// HO.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/pkg/errors"
	"github.com/tmccombs/hcl2json/convert"
	"github.com/yuin/goldmark"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

const (
	extMarkdown    = ".markdown"
	blockResource  = "resource"
	keySubCategory = "subcategory"
	keyDescription = "description"
	keyPageTitle   = "page_title"
)

type ProviderMetadata struct {
	Name          string               `yaml:"name"`
	Resources     map[string]*Resource `yaml:"resources"`
	codeXPath     string
	preludeXPath  string
	fieldDocXPath string
}

func NewProviderMetadata(name, codeXPath, preludeXPath, fieldPathXPath string) *ProviderMetadata {
	return &ProviderMetadata{
		Name:          name,
		Resources:     make(map[string]*Resource),
		codeXPath:     codeXPath,
		preludeXPath:  preludeXPath,
		fieldDocXPath: fieldPathXPath,
	}
}

type ResourceExample struct {
	Manifest   string            `yaml:"manifest"`
	References map[string]string `yaml:"references"`
}

type Resource struct {
	SubCategory  string            `yaml:"subCategory"`
	Description  string            `yaml:"description"`
	Name         string            `yaml:"name"`
	TitleName    string            `yaml:"titleName"`
	Examples     []ResourceExample `yaml:"examples"`
	ArgumentDocs map[string]string `yaml:"argumentDocs"`
}

func (r *Resource) addExampleManifest(body *hclsyntax.Block, manifest []byte) error {
	out := bytes.Buffer{}
	err := json.Indent(&out, manifest, "", "  ")
	if err != nil {
		return errors.Wrap(err, "unable to format JSON example manifest")
	}

	refs, err := r.findReferences(body)
	if err != nil {
		return err
	}
	r.Examples = append(r.Examples, ResourceExample{
		Manifest:   out.String(),
		References: refs,
	})
	return nil
}

func (r *Resource) AddArgumentDoc(fieldName, doc string) {
	if r.ArgumentDocs == nil {
		r.ArgumentDocs = make(map[string]string)
	}
	r.ArgumentDocs[fieldName] = strings.TrimSpace(doc)
}

func (r *Resource) scrapeExamples(doc *html.Node, codeElXPath string) error {
	resourceName := r.TitleName
	nodes := htmlquery.Find(doc, codeElXPath)
	for _, n := range nodes {
		parser := hclparse.NewParser()
		f, diag := parser.ParseHCL([]byte(n.Data), "example.hcl")
		if diag.HasErrors() {
			return errors.Wrap(diag, "failed to parse example Terraform configuration")
		}

		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			return errors.Errorf("not an HCL Body: %s", n.Data)
		}
		trimmed := make(hclsyntax.Blocks, 0, len(body.Blocks))
		for _, b := range body.Blocks {
			if b.Type == blockResource {
				trimmed = append(trimmed, b)
			}
		}
		body.Blocks = trimmed
		// first try an exact match to find the example
		if err := r.findExampleBlock(f, body.Blocks, &resourceName, true); err != nil {
			return err
		}
		r.Name = resourceName
	}

	return nil
}

func (r *Resource) findReferences(b *hclsyntax.Block) (map[string]string, error) {
	refs := make(map[string]string)
	if b.Labels[0] != r.Name {
		return refs, nil
	}
	for name, attr := range b.Body.Attributes {
		ref := ""
		switch e := attr.Expr.(type) {
		case *hclsyntax.ScopeTraversalExpr:
			if len(e.Traversal) < 2 {
				return refs, nil
			}
			tr, ok := e.Traversal[0].(hcl.TraverseRoot)
			if !ok {
				return refs, nil
			}
			ta, ok := e.Traversal[len(e.Traversal)-1].(hcl.TraverseAttr)
			if !ok {
				return refs, nil
			}
			ref = fmt.Sprintf("%s.%s", tr.Name, ta.Name)
		}
		if ref == "" {
			return refs, nil
		}
		if v, ok := refs[name]; ok && v != ref {
			return nil, errors.Errorf("attribute %s.%s refers to %s. New reference: %s", r.Name, name, v, ref)
		}
		refs[name] = ref
	}
	return refs, nil
}

func suffixMatch(label, resourceName string, limit int) bool {
	suffixParts := strings.Split(resourceName, "_")
	for i := 0; i < len(suffixParts) && (limit == -1 || i <= limit); i++ {
		s := strings.Join(suffixParts[i:], "_")
		if strings.Contains(label, s) {
			return true
		}
	}
	return false
}

func (r *Resource) findExampleBlock(file *hcl.File, blocks hclsyntax.Blocks, resourceName *string, exactMatch bool) error {
	for _, b := range blocks {
		if b.Labels[0] != *resourceName {
			if exactMatch {
				continue
			}

			if suffixMatch(b.Labels[0], *resourceName, 1) {
				*resourceName = b.Labels[0]
				exactMatch = true
			} else {
				continue
			}
		}

		buff, err := convert.File(&hcl.File{
			Body:  b.Body,
			Bytes: file.Bytes,
		}, convert.Options{})
		if err != nil {
			return errors.Wrap(err, "failed to format as JSON")
		}

		r.Name = *resourceName
		err = r.addExampleManifest(b, buff)
		if err != nil {
			return errors.Wrap(err, "failed to add example manifest to resource")
		}
	}

	if len(r.Examples) == 0 && exactMatch {
		return r.findExampleBlock(file, blocks, resourceName, false)
	}
	return nil
}

func (r *Resource) scrapePrelude(doc *html.Node, preludeXPath string) error {
	// parse prelude
	nodes := htmlquery.Find(doc, preludeXPath)
	if len(nodes) > 0 {
		n := nodes[0]
		lines := strings.Split(n.Data, "\n")
		descIndex := -1
		for i, l := range lines {
			kv := strings.Split(l, ":")
			if len(kv) < 2 {
				continue
			}
			switch kv[0] {
			case keyPageTitle:
				r.TitleName = strings.TrimSpace(strings.ReplaceAll(kv[len(kv)-1], `"`, ""))

			case keyDescription:
				r.Description = kv[1]
				descIndex = i

			case keySubCategory:
				r.SubCategory = strings.TrimSpace(strings.ReplaceAll(kv[1], `"`, ""))
			}
		}

		if descIndex > -1 {
			r.Description += strings.Join(lines[descIndex+1:], " ")
		}
		r.Description = strings.TrimSpace(strings.Replace(r.Description, "|-", "", 1))
	}

	if r.Description == "" || r.SubCategory == "" || r.TitleName == "" {
		return errors.Errorf("failed to parse prelude. Description: %s, Subcategory: %s, Title name: %s",
			r.Description, r.SubCategory, r.TitleName)
	}
	return nil
}

func (r *Resource) scrapeFieldDocs(doc *html.Node, fieldXPath string) {
	processed := make(map[*html.Node]struct{})
	codeNodes := htmlquery.Find(doc, fieldXPath)
	for _, n := range codeNodes {
		attrName := ""
		doc := r.scrapeDocString(n, &attrName, processed)
		if doc == "" {
			continue
		}
		r.AddArgumentDoc(attrName, doc)
	}
}

func (r *Resource) scrapeDocString(n *html.Node, attrName *string, processed map[*html.Node]struct{}) string {
	if _, ok := processed[n]; ok {
		return ""
	}
	processed[n] = struct{}{}

	if n.Type == html.ElementNode {
		return r.scrapeDocString(n.FirstChild, attrName, processed)
	}

	sb := strings.Builder{}
	if *attrName == "" {
		*attrName = n.Data
	} else {
		sb.WriteString(n.Data)
	}
	s := n.Parent
	for s = s.NextSibling; s != nil; s = s.NextSibling {
		if _, ok := processed[s]; ok {
			continue
		}
		processed[s] = struct{}{}

		switch s.Type {
		case html.TextNode:
			sb.WriteString(s.Data)
		case html.ElementNode:
			if s.FirstChild == nil {
				continue
			}
			sb.WriteString(r.scrapeDocString(s.FirstChild, attrName, processed))
		}
	}
	return sb.String()
}

// Scrape scrapes resource metadata from the specified HTML doc.
// filename is not always the precise resource name, hence,
// it returns the resource name scraped from the doc.
func (r *Resource) Scrape(path, codeElXPath, preludeXPath, docXPath string) error {
	source, err := ioutil.ReadFile(path)
	if err != nil {
		return errors.Wrap(err, "failed to read markdown file")
	}

	var buff bytes.Buffer
	if err := goldmark.Convert(source, &buff); err != nil {
		return errors.Wrap(err, "failed to convert markdown")
	}

	doc, err := htmlquery.Parse(&buff)
	if err != nil {
		return errors.Wrap(err, "failed to parse HTML")
	}

	if err := r.scrapePrelude(doc, preludeXPath); err != nil {
		return err
	}

	r.scrapeFieldDocs(doc, docXPath)

	return r.scrapeExamples(doc, codeElXPath)
}

func (pm *ProviderMetadata) ScrapeRepo(path string) error {
	return errors.Wrap(filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "failed to traverse Terraform registry")
		}
		if d.IsDir() || filepath.Ext(d.Name()) != extMarkdown {
			return nil
		}
		r := &Resource{}
		if err := r.Scrape(path, pm.codeXPath, pm.preludeXPath, pm.fieldDocXPath); err != nil {
			return errors.Wrap(err, "failed to scrape resource metadata")
		}

		pm.Resources[r.Name] = r
		return nil
	}), "cannot scrape Terraform registry")
}

func main() {
	pm := NewProviderMetadata("hashicorp/terraform-provider-azurerm",
		`//code[@class="language-terraform" or @class="language-hcl"]/text()`,
		`//text()[contains(., "description") and contains(., "subcategory")]`,
		`//ul/li//code[1]/text()`)

	//err := pm.ScrapeRepo("/tmp/scrape/")
	err := pm.ScrapeRepo("/Users/alper/data/workspaces/github.com/hashicorp/terraform-provider-azurerm/website/docs/r")
	if err != nil {
		panic(err)
	}

	out, err := yaml.Marshal(pm)
	if err != nil {
		panic(err)
	}

	if err := ioutil.WriteFile("provider-metadata.yaml", out, 0644); err != nil {
		panic(err)
	}
}
