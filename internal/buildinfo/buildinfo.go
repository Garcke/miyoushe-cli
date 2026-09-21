// Package buildinfo exposes version metadata injected by release builds.
package buildinfo

import (
	"fmt"
	"strings"
)

var (
	// Version is the semantic version without a leading v. Development builds
	// keep the value "dev".
	Version = "dev"
	// Commit is the source commit used for the build.
	Commit = "none"
	// Date is the reproducible commit date supplied by the release builder.
	Date = "unknown"
)

// String returns stable human-readable build metadata for Cobra's --version
// flag. The version remains the first token so installers can verify it.
func String() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}
	var details []string
	if commit := strings.TrimSpace(Commit); commit != "" && commit != "none" {
		details = append(details, "commit "+commit)
	}
	if date := strings.TrimSpace(Date); date != "" && date != "unknown" {
		details = append(details, "built "+date)
	}
	if len(details) == 0 {
		return version
	}
	return fmt.Sprintf("%s (%s)", version, strings.Join(details, ", "))
}
