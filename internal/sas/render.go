package sas

import (
	"fmt"
	"html"
	"strings"
)

const reportCSS = `
body{font-family:Arial,Helvetica,sans-serif;margin:0;color:#000;background:#fafbfe}
header{background:#1f3a68;color:#fff;padding:10px 20px;display:flex;align-items:center;gap:24px}
header h1{font-size:18px;margin:0}
header nav a{color:#cfe0ff;margin-right:16px;text-decoration:none;font-size:14px}
header nav a:hover{color:#fff}
main{padding:12px 24px}
.section{background:#fff;border:1px solid #d7dde8;border-radius:4px;margin:16px auto;padding:12px 18px;max-width:1100px}
.titles{text-align:center;margin:4px 0 8px}
.titles .t1{font-size:16px;font-weight:bold}
.titles .tn{font-size:14px;font-weight:bold}
.proc{text-align:center;font-size:14px;font-weight:bold;color:#112277;margin:6px 0 10px}
.byline{text-align:center;font-weight:bold;margin:14px 0 6px;color:#112277}
.footnotes{text-align:center;font-size:12px;margin-top:8px}
table.sas{border-collapse:collapse;margin:10px auto 14px;font-size:13px}
table.sas caption{font-weight:bold;padding:4px;color:#112277}
table.sas th,table.sas td{border:1px solid #c1c1c1;padding:3px 8px}
table.sas thead th{background:#edf2f9;color:#112277;font-weight:bold;text-align:center}
table.sas tbody th{background:#edf2f9;color:#112277;font-weight:normal;text-align:left;white-space:nowrap}
table.sas td{text-align:right;white-space:nowrap}
table.sas td.c{text-align:left}
.notes{font-size:12px;text-align:center;color:#333;margin:-8px 0 12px}
.graph{text-align:center;margin:10px 0}
.graph svg{max-width:100%;height:auto;border:1px solid #e3e6eb}
pre.text{font-family:Menlo,Consolas,monospace;font-size:12px;background:#fff;margin:8px auto;width:fit-content}
#log pre{font-family:Menlo,Consolas,monospace;font-size:12px;line-height:1.35;background:#fff;border:1px solid #d7dde8;padding:12px;max-width:1100px;margin:16px auto;white-space:pre-wrap}
.lnote{color:#1d3c9e}.lwarn{color:#1f8a1f}.lerr{color:#c00000;font-weight:bold}.lsrc{color:#000}.lput{color:#000}
.summary{max-width:1100px;margin:8px auto;font-size:13px}
.summary .err{color:#c00000;font-weight:bold}
`

// RenderHTML renders results and log as a standalone HTML document.
func (s *Session) RenderHTML(title string) string {
	var b strings.Builder
	esc := html.EscapeString
	fmt.Fprintf(&b, "<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"><title>%s</title><style>%s</style></head><body>\n", esc(title), reportCSS)
	fmt.Fprintf(&b, "<header><h1>sago &mdash; %s</h1><nav><a href=\"#results\">Results</a><a href=\"#log\">Log</a></nav></header>\n<main>\n", esc(title))
	status := fmt.Sprintf("%d error(s), %d warning(s)", s.Log.Errors, s.Log.Warns)
	cls := ""
	if s.Log.Errors > 0 {
		cls = " class=\"err\""
	}
	fmt.Fprintf(&b, "<div class=\"summary\"><span%s>%s</span> &mdash; see <a href=\"#log\">Log</a></div>\n", cls, status)
	b.WriteString("<div id=\"results\">\n")
	for _, sec := range s.Out.Sections {
		if len(sec.Items) == 0 {
			continue
		}
		b.WriteString("<div class=\"section\">\n")
		if len(sec.Titles) > 0 {
			b.WriteString("<div class=\"titles\">")
			for i, t := range sec.Titles {
				c := "tn"
				if i == 0 {
					c = "t1"
				}
				fmt.Fprintf(&b, "<div class=\"%s\">%s</div>", c, esc(t))
			}
			b.WriteString("</div>\n")
		}
		if sec.Proc != "" && !s.NoProcTitle {
			fmt.Fprintf(&b, "<div class=\"proc\">%s</div>\n", esc(sec.Proc))
		}
		for _, it := range sec.Items {
			switch x := it.(type) {
			case *Table:
				b.WriteString(renderTable(x))
			case *Graph:
				b.WriteString("<div class=\"graph\">")
				if x.Caption != "" {
					fmt.Fprintf(&b, "<div class=\"proc\">%s</div>", esc(x.Caption))
				}
				b.WriteString(x.SVG)
				b.WriteString("</div>\n")
			case *Text:
				switch {
				case x.Class == "byline":
					fmt.Fprintf(&b, "<div class=\"byline\">%s</div>\n", esc(x.Text))
				case x.Pre:
					fmt.Fprintf(&b, "<pre class=\"text\">%s</pre>\n", esc(x.Text))
				default:
					fmt.Fprintf(&b, "<p class=\"notes\">%s</p>\n", esc(x.Text))
				}
			case *HTML:
				b.WriteString(x.HTML)
				b.WriteString("\n")
			}
		}
		if len(sec.Footnotes) > 0 {
			b.WriteString("<div class=\"footnotes\">")
			for _, t := range sec.Footnotes {
				fmt.Fprintf(&b, "<div>%s</div>", esc(t))
			}
			b.WriteString("</div>\n")
		}
		b.WriteString("</div>\n")
	}
	b.WriteString("</div>\n<div id=\"log\"><pre>")
	for _, e := range s.Log.Entries {
		c := map[LogLevel]string{LogSource: "lsrc", LogNote: "lnote", LogWarn: "lwarn", LogError: "lerr", LogPut: "lput"}[e.Level]
		fmt.Fprintf(&b, "<span class=\"%s\">%s</span>\n", c, esc(e.String()))
	}
	b.WriteString("</pre></div>\n</main></body></html>\n")
	return b.String()
}

func renderTable(t *Table) string {
	var b strings.Builder
	esc := html.EscapeString
	b.WriteString("<table class=\"sas\">")
	if t.Caption != "" {
		fmt.Fprintf(&b, "<caption>%s</caption>", esc(t.Caption))
	}
	var vis []int
	for i, c := range t.Cols {
		if !c.Hidden {
			vis = append(vis, i)
		}
	}
	b.WriteString("<thead>")
	if len(t.Spans) > 0 {
		b.WriteString("<tr>")
		for k := 0; k < len(vis); {
			span := (*Span)(nil)
			for si := range t.Spans {
				if t.Spans[si].From == k {
					span = &t.Spans[si]
				}
			}
			if span != nil {
				fmt.Fprintf(&b, "<th colspan=\"%d\">%s</th>", span.To-span.From+1, esc(span.Label))
				k = span.To + 1
				continue
			}
			b.WriteString("<th></th>")
			k++
		}
		b.WriteString("</tr>")
	}
	allEmpty := true
	for _, i := range vis {
		if t.Cols[i].Label != "" {
			allEmpty = false
		}
	}
	if !allEmpty {
		b.WriteString("<tr>")
		for _, i := range vis {
			fmt.Fprintf(&b, "<th>%s</th>", esc(t.Cols[i].Label))
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</thead><tbody>")
	for r := range t.Rows {
		b.WriteString("<tr>")
		for k, i := range vis {
			if i >= len(t.Rows[r]) {
				continue
			}
			cell := esc(t.Cell(r, i))
			if k < t.RowHeader {
				fmt.Fprintf(&b, "<th>%s</th>", cell)
			} else if t.Rows[r][i].Char {
				fmt.Fprintf(&b, "<td class=\"c\">%s</td>", cell)
			} else {
				fmt.Fprintf(&b, "<td>%s</td>", cell)
			}
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>\n")
	for _, n := range t.Notes {
		fmt.Fprintf(&b, "<p class=\"notes\">%s</p>\n", esc(n))
	}
	return b.String()
}
