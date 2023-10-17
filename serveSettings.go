// Ignore this for now, needs more work.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
)

/*
{
	"localhost\.com": {
	  "redirects": {
		"(.*)/path/to/url": {
		  "NewURL": "{1}/newPath/to/url",
		  "Status": 200,
		  "Values": [0]
		},
		"(.*)/path/to/url?query=([0-9]+?)(.*)": {
		  "NewURL": "{1}/newPath/to/url?query={2}{3}",
		  "Status": 301,
		  "Values": [0,1,2]
		},
		"http://localhost/path/to/url?(.*)": {
		  "NewURL": "http://localhost/newPath/to/url?{1}",
		  "Status": 301,
		  "Values": [0]
		},
		"http://localhost/path/to/url?query=100&test=2": {
		  "NewURL": "http://localhost/newPath/to/url",
		  "Status": 301,
		  "Values": []
		}
	  }
	}
  }
`
*/

type GameConfig struct {
	DomainsRegex map[string]regexp.Regexp
	Domains      map[string]Domain
}

type Domain struct {
	RedirectList Redirect `json:"redirects"`
}
type Redirect struct {
	RedirectsRegex map[string]regexp.Regexp
	Redirects      map[string]Destination
}
type Destination struct {
	NewURL string `json:"NewURL"`
	Status int    `json:"Status"`
	Values []int  `json:"Values"`
}

func (d *Destination) GenerateNewURL(rURL *url.URL) *url.URL {
	return nil
}

func (gc *GameConfig) FindRedirect(rURL *url.URL) (*Destination, *regexp.Regexp) {
	var res *Destination
	var redRegex *regexp.Regexp

	for d := range gc.Domains {
		//If the regex doesn't match, continue with the next
		domRegex := gc.DomainsRegex[d]
		if !domRegex.MatchString(rURL.Host) {
			continue
		}

		redirList := gc.Domains[d].RedirectList
		for r := range redirList.Redirects {
			//If the regex doesn't match, continue with the next
			*redRegex = redirList.RedirectsRegex[r]
			if !redRegex.MatchString(rURL.String()) {
				continue
			}

			*res = redirList.Redirects[r]
		}
	}
	return res, redRegex
}

func (gc *GameConfig) UnmarshalJSON(data []byte) error {
	var v []interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	fmt.Println(v)

	return nil
}
