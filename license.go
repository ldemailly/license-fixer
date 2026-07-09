// Package main implements logic for detecting and fixing unfilled Apache
// License copyright placeholders.
package main

import "strings"

// copyrightTemplate is the unfilled placeholder present in the standard
// Apache 2.0 LICENSE boilerplate.
const copyrightTemplate = "   Copyright [yyyy] [name of copyright owner]"

// isLicenseBroken reports whether content still contains the unfilled
// Apache copyright placeholder.
func isLicenseBroken(content string) bool {
	return strings.Contains(content, copyrightTemplate)
}

// fixedLicense replaces the copyright placeholder in content with one line
// per entry in copyrights, each prefixed with "   Copyright ".
// If copyrights is empty, the original content is returned unchanged.
func fixedLicense(content string, copyrights []string) string {
	if len(copyrights) == 0 {
		return content
	}
	var sb strings.Builder
	for i, c := range copyrights {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("   Copyright ")
		sb.WriteString(c)
	}
	return strings.ReplaceAll(content, copyrightTemplate, sb.String())
}
