// Command sago runs SAS programs and writes an HTML report.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"sago/internal/sas"
)

func main() {
	out := flag.String("o", "", "output HTML file (default: <program>.html next to the program; '-' for stdout)")
	quiet := flag.Bool("q", false, "do not echo the SAS log to stderr")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: sago [-o report.html] [-q] program.sas\n\nRuns a SAS program and writes the results (tables, graphs) and log to an HTML file.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	path := flag.Arg(0)
	var src []byte
	var err error
	dir := "."
	if path == "-" {
		src, err = io.ReadAll(os.Stdin)
	} else {
		src, err = os.ReadFile(path)
		dir = filepath.Dir(path)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sago:", err)
		os.Exit(2)
	}
	absDir, _ := filepath.Abs(dir)
	s := sas.NewSession(absDir)
	if !*quiet {
		s.Log.Echo = func(e sas.LogEntry) {
			if e.Level != sas.LogSource {
				fmt.Fprintln(os.Stderr, e.String())
			}
		}
	}
	s.RunProgram(string(src))

	name := "program"
	if path != "-" {
		name = filepath.Base(path)
	}
	report := s.RenderHTML(name)
	dest := *out
	if dest == "" {
		if path == "-" {
			dest = "-"
		} else {
			dest = strings.TrimSuffix(path, filepath.Ext(path)) + ".html"
		}
	}
	if dest == "-" {
		fmt.Print(report)
	} else {
		if err := os.WriteFile(dest, []byte(report), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "sago:", err)
			os.Exit(2)
		}
		if !*quiet {
			fmt.Fprintf(os.Stderr, "sago: wrote %s (%d errors, %d warnings)\n", dest, s.Log.Errors, s.Log.Warns)
		}
	}
	if s.Log.Errors > 0 {
		os.Exit(1)
	}
}
