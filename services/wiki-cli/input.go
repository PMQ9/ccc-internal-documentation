package main

import (
	"io"
	"os"
)

// readSource resolves a body source that can be given as a literal flag value or read
// from a file (with "-" meaning stdin). literal and file are mutually exclusive. It
// returns the content and whether a source was provided at all. These inputs are the
// operator's own files at the operator's privilege — there is no untrusted-path boundary
// to defend, and BookStack sanitizes the content on render, so the CLI transports it as-is.
//
// NOTE: a "-" source consumes stdin, which can only be read once. Each handler calls this
// at most once with "-" (markdown XOR html is enforced before this is reached), so the
// single-stream read is safe.
func readSource(literal, file string, stdin io.Reader) (content string, provided bool, err error) {
	switch {
	case literal != "" && file != "":
		return "", false, usagef("provide either the literal value or the --*-file form, not both")
	case file == "-":
		b, rerr := io.ReadAll(stdin)
		if rerr != nil {
			return "", false, usagef("read body from stdin: %v", rerr)
		}
		return string(b), true, nil
	case file != "":
		b, rerr := os.ReadFile(file)
		if rerr != nil {
			return "", false, usagef("read body file %s: %v", file, rerr)
		}
		return string(b), true, nil
	case literal != "":
		return literal, true, nil
	default:
		return "", false, nil
	}
}

// openFileArg opens the file at path for an upload and returns its base name (the
// multipart filename). The caller must Close the returned reader.
func openFileArg(path string) (io.ReadCloser, string, error) {
	if path == "" {
		return nil, "", usagef("--file is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", usagef("open %s: %v", path, err)
	}
	return f, baseName(path), nil
}

// baseName returns the last path element (the filename), handling both separators.
func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}
