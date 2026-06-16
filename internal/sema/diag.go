package sema

import (
	"fmt"
	"sort"
)

// Diag mirrors lexer/parser Diag shape so the CLI can print
// sema errors in the same `<file>:<line>:<col>: error[code]: msg`
// format. Codes come from lang-spec/diagnostics.md.
type Diag struct {
	File    string
	Code    string
	Message string
	Line    int
	Col     int
}

func (d *Diag) Error() string {
	if d.File == "" {
		return fmt.Sprintf("%d:%d: error[%s]: %s", d.Line, d.Col, d.Code, d.Message)
	}
	return fmt.Sprintf("%s:%d:%d: error[%s]: %s", d.File, d.Line, d.Col, d.Code, d.Message)
}

// sortDiags by (file, line, col, code). See docs/internals/sema.md §8
// #4. File is the primary key so a multi-file package (RFC-0002) reports
// each file's diagnostics together; it is a no-op for a single file.
func sortDiags(ds []*Diag) {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].File != ds[j].File {
			return ds[i].File < ds[j].File
		}
		if ds[i].Line != ds[j].Line {
			return ds[i].Line < ds[j].Line
		}
		if ds[i].Col != ds[j].Col {
			return ds[i].Col < ds[j].Col
		}
		return ds[i].Code < ds[j].Code
	})
}
