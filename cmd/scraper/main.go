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

package main

import (
	"os"
	"path/filepath"

	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/ulucinar/terraform-registry-scraper/pkg/meta"
)

func main() {
	var (
		app                   = kingpin.New(filepath.Base(os.Args[0]), "Terraform Registry provider metadata scraper.").DefaultEnvars()
		outFile               = app.Flag("out", "Provider metadata output file path").Short('o').Default("provider-metadata.yaml").OpenFile(os.O_CREATE, 0644)
		providerName          = app.Flag("name", "Provider name").Short('n').Required().String()
		codeXPath             = app.Flag("code-xpath", "Code XPath expression").Default(`//code[@class="language-terraform" or @class="language-hcl"]/text()`).String()
		preludeXPath          = app.Flag("prelude-xpath", "Prelude XPath expression").Default(`//text()[contains(., "description") and contains(., "subcategory")]`).String()
		fieldXPath            = app.Flag("field-xpath", "Field XPath expression").Default(`//ul/li//code[1]/text()`).String()
		importXPath           = app.Flag("import-xpath", "Import statements XPath expression").Default(`//code[@class="language-shell"]/text()`).String()
		repoPath              = app.Flag("repo", "Terraform provider repo path").Short('r').Required().ExistingDir()
		skipExampleErrors     = app.Flag("skip-example-errors", "Skip errors encountered while parsing example manifests").Default("false").Bool()
		skipExampleReferences = app.Flag("skip-example-refs", "Skip parsing references in example manifests").Default("false").Bool()
	)
	kingpin.MustParse(app.Parse(os.Args[1:]))

	pm := meta.NewProviderMetadata(*providerName, *codeXPath, *preludeXPath, *fieldXPath, *importXPath)
	kingpin.FatalIfError(pm.ScrapeRepo(&meta.ScrapeConfiguration{
		SkipExampleErrors:     *skipExampleErrors,
		SkipExampleReferences: *skipExampleReferences,
		RepoPath:              *repoPath,
	}), "Failed to scrape Terraform provider metadata")
	kingpin.FatalIfError(pm.Store((*outFile).Name()), "Failed to store Terraform provider metadata to file: %s", (*outFile).Name())
}
