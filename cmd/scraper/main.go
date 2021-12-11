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
	"io/ioutil"

	"gopkg.in/yaml.v3"

	"github.com/ulucinar/terraform-registry-scraper/pkg/meta"
)

func main() {
	pm := meta.NewProviderMetadata("hashicorp/terraform-provider-azurerm",
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
