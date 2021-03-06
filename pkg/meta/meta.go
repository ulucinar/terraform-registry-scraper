// HO.

/*
Copyright 2021 Alper Rifat Ulucinar
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package meta

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
	importXPath   string
}

func NewProviderMetadata(name, codeXPath, preludeXPath, fieldPathXPath, importXPath string) *ProviderMetadata {
	return &ProviderMetadata{
		Name:          name,
		Resources:     make(map[string]*Resource),
		codeXPath:     codeXPath,
		preludeXPath:  preludeXPath,
		fieldDocXPath: fieldPathXPath,
		importXPath:   importXPath,
	}
}

func NewProviderMetadataFromFile(path string) (*ProviderMetadata, error) {
	buff, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read metadata file %q", path)
	}

	metadata := &ProviderMetadata{}
	return metadata, errors.Wrap(yaml.Unmarshal(buff, metadata), "failed to unmarshal provider metadata")
}

type Dependencies map[string]string

type ResourceExample struct {
	Name         string            `yaml:"name"`
	Manifest     string            `yaml:"manifest"`
	References   map[string]string `yaml:"references,omitempty"`
	Dependencies Dependencies      `yaml:"dependencies,omitempty"`
}

type Resource struct {
	SubCategory      string            `yaml:"subCategory"`
	Description      string            `yaml:"description,omitempty"`
	Name             string            `yaml:"name"`
	TitleName        string            `yaml:"titleName"`
	Examples         []ResourceExample `yaml:"examples,omitempty"`
	ArgumentDocs     map[string]string `yaml:"argumentDocs"`
	ImportStatements []string          `yaml:"importStatements"`
	scrapeConfig     *ScrapeConfiguration
}

func (r *Resource) addExampleManifest(file *hcl.File, body *hclsyntax.Block) error {
	refs, err := r.findReferences(file, body)
	if err != nil {
		return err
	}
	r.Examples = append(r.Examples, ResourceExample{
		Name:       body.Labels[1],
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
			err := errors.Wrapf(diag, "failed to parse example Terraform configuration. Configuration:\n%s", n.Data)
			if !r.scrapeConfig.SkipExampleErrors {
				return err
			}
			fmt.Printf("%v\n", err)
			continue
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

	if r.Name == "" {
		r.Name = resourceName
	}
	return nil
}

func (r *Resource) findReferences(file *hcl.File, b *hclsyntax.Block) (map[string]string, error) {
	if r.scrapeConfig.SkipExampleReferences {
		return map[string]string{}, nil
	}
	refs := make(map[string]string)
	if b.Labels[0] != r.Name {
		return refs, nil
	}
	for name, attr := range b.Body.Attributes {
		ref := ""
		switch e := attr.Expr.(type) {
		case *hclsyntax.ScopeTraversalExpr:
			ref = string(file.Bytes[e.Range().Start.Byte:e.Range().End.Byte])
		}
		if ref == "" {
			continue
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

func convertManifest2JSON(file *hcl.File, b *hclsyntax.Block) (string, error) {
	buff, err := convert.File(&hcl.File{
		Body:  b.Body,
		Bytes: file.Bytes,
	}, convert.Options{})
	if err != nil {
		return "", errors.Wrap(err, "failed to format as JSON")
	}
	out := bytes.Buffer{}
	err = json.Indent(&out, buff, "", "  ")
	if err != nil {
		return "", errors.Wrap(err, "unable to format JSON example manifest")
	}
	return out.String(), nil
}

func (r *Resource) findExampleBlock(file *hcl.File, blocks hclsyntax.Blocks, resourceName *string, exactMatch bool) error {
	dependencies := make(map[string]string)
	for _, b := range blocks {
		depKey := fmt.Sprintf("%s.%s", b.Labels[0], b.Labels[1])
		m, err := convertManifest2JSON(file, b)
		if err != nil {
			return errors.Wrap(err, "failed to convert example manifest to JSON")
		}
		if b.Labels[0] != *resourceName {
			if exactMatch {
				dependencies[depKey] = m
				continue
			}

			if suffixMatch(b.Labels[0], *resourceName, 1) {
				*resourceName = b.Labels[0]
				exactMatch = true
			} else {
				dependencies[depKey] = m
				continue
			}
		}
		r.Name = *resourceName
		err = r.addExampleManifest(file, b)
		r.Examples[len(r.Examples)-1].Manifest = m
		r.Examples[len(r.Examples)-1].Dependencies = dependencies
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
	rawData := ""
	if len(nodes) > 0 {
		n := nodes[0]
		rawData = n.Data
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

	if r.SubCategory == "" || r.TitleName == "" {
		return errors.Errorf("failed to parse prelude. Description: %s, Subcategory: %s, Title name: %s. Raw data:%s\n",
			r.Description, r.SubCategory, r.TitleName, rawData)
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

func (r *Resource) scrapeImportStatements(doc *html.Node, importXPath string) {
	nodes := htmlquery.Find(doc, importXPath)
	for _, n := range nodes {
		r.ImportStatements = append(r.ImportStatements, strings.TrimSpace(n.Data))
	}
}

// scrape scrapes resource metadata from the specified HTML doc.
// filename is not always the precise resource name, hence,
// it returns the resource name scraped from the doc.
func (r *Resource) scrape(path, codeElXPath, preludeXPath, docXPath, importXPath string) error {
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
	r.scrapeImportStatements(doc, importXPath)

	return r.scrapeExamples(doc, codeElXPath)
}

type ScrapeConfiguration struct {
	SkipExampleErrors     bool
	SkipExampleReferences bool
	RepoPath              string
}

func (pm *ProviderMetadata) ScrapeRepo(config *ScrapeConfiguration) error {
	return errors.Wrap(filepath.WalkDir(config.RepoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.Wrap(err, "failed to traverse Terraform registry")
		}
		if d.IsDir() || filepath.Ext(d.Name()) != extMarkdown {
			return nil
		}
		r := &Resource{
			scrapeConfig: config,
		}
		if err := r.scrape(path, pm.codeXPath, pm.preludeXPath, pm.fieldDocXPath, pm.importXPath); err != nil {
			return errors.Wrap(err, "failed to scrape resource metadata")
		}

		pm.Resources[r.Name] = r
		return nil
	}), "cannot scrape Terraform registry")
}

func (pm *ProviderMetadata) Store(path string) error {
	out, err := yaml.Marshal(pm)
	if err != nil {
		return errors.Wrap(err, "failed to marshal provider metadata to YAML")
	}
	return errors.Wrapf(ioutil.WriteFile(path, out, 0644), "failed to write provider metada file: %s", path)
}
