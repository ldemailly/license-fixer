package main

import (
	"strings"
	"testing"
)

// apacheLicense is a representative snippet of the Apache 2.0 LICENSE file
// with the unfilled copyright placeholder at the bottom.
const apacheLicense = `                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   [... snip ...]

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

   Copyright [yyyy] [name of copyright owner]

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
`

const filledLicense = `                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   [... snip ...]

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

   Copyright 2025 Fortio Authors

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
`

func TestIsLicenseBroken(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"unfilled template", apacheLicense, true},
		{"filled single", filledLicense, false},
		{"filled multi", strings.Replace(apacheLicense, copyrightTemplate,
			"   Copyright 2016 Fortio Authors\n   Copyright 2015 Michal Witkowski", 1), false},
		{"empty", "", false},
		{"unrelated text", "hello world", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLicenseBroken(tt.content); got != tt.want {
				t.Errorf("isLicenseBroken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFixedLicense(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		copyrights []string
		want       string
	}{
		{
			name:       "single copyright",
			content:    apacheLicense,
			copyrights: []string{"2025 Fortio Authors"},
			want:       filledLicense,
		},
		{
			name:    "multiple copyrights",
			content: apacheLicense,
			copyrights: []string{
				"2016 Fortio Authors",
				"2015 Michal Witkowski",
			},
			want: strings.Replace(apacheLicense, copyrightTemplate,
				"   Copyright 2016 Fortio Authors\n   Copyright 2015 Michal Witkowski", 1),
		},
		{
			name:       "no copyrights returns unchanged",
			content:    apacheLicense,
			copyrights: nil,
			want:       apacheLicense,
		},
		{
			name:       "already filled content is not changed",
			content:    filledLicense,
			copyrights: []string{"2025 Fortio Authors"},
			want:       filledLicense, // no template to replace
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fixedLicense(tt.content, tt.copyrights)
			if got != tt.want {
				t.Errorf("fixedLicense() =\n%q\nwant\n%q", got, tt.want)
			}
			// After fixing, the result should not be broken (unless no copyrights given).
			if len(tt.copyrights) > 0 && isLicenseBroken(got) {
				t.Errorf("fixedLicense() result still contains template placeholder")
			}
		})
	}
}

func TestFixedLicenseRoundTrip(t *testing.T) {
	fixed := fixedLicense(apacheLicense, []string{"2025 Fortio Authors"})
	if isLicenseBroken(fixed) {
		t.Error("after fixedLicense(), isLicenseBroken() should return false")
	}
}
