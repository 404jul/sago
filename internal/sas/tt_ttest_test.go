package sas

import (
	"testing"
)

// ttTables runs a program and returns the displayed tables by ODS name.
func ttTables(t *testing.T, src string) map[string][]*Table {
	t.Helper()
	s := NewSession(t.TempDir())
	s.RunProgram(src)
	out := map[string][]*Table{}
	for _, sec := range s.Out.Sections {
		for _, it := range sec.Items {
			if tb, ok := it.(*Table); ok {
				out[tb.ODSName] = append(out[tb.ODSName], tb)
			}
			if h, ok := it.(*HTML); ok && h.Table != nil {
				out[h.Table.ODSName] = append(out[h.Table.ODSName], h.Table)
			}
		}
	}
	return out
}

func ttCell(tb *Table, r int, col string) float64 {
	for i, c := range tb.Cols {
		if c.Name == col {
			return tb.Rows[r][i].Num
		}
	}
	return -999
}

// References: scipy.stats ttest_ind (pooled / Welch), ttest_1samp, chi2.ppf
// SD limits, f.sf; SAS doc: F 60.5889 5.0183, M 63.9100 4.9379, t=-1.45 p=0.1645.
func TestTTClassHeight(t *testing.T) {
	tb := ttTables(t, "proc ttest data=sashelp.class plots=none; class sex; var height; run;"+
		"proc ttest data=sashelp.class h0=60 plots=none; var height; run;")
	st := tb["Statistics"][0]
	uvClose(t, "mean F", ttCell(st, 0, "Mean"), 60.5888889, 1e-7)
	uvClose(t, "sd M", ttCell(st, 1, "StdDev"), 4.9379, 1e-4)
	tt := tb["TTests"][0]
	uvClose(t, "t pooled", ttCell(tt, 0, "tValue"), -1.4526253832215752, 1e-10)
	uvClose(t, "p pooled", ttCell(tt, 0, "Probt"), 0.16453634061120392, 1e-9)
	uvClose(t, "df satt", ttCell(tt, 1, "DF"), 16.72694842289225, 1e-10)
	uvClose(t, "p satt", ttCell(tt, 1, "Probt"), 0.1651879946299238, 1e-9)
	cl := tb["ConfLimits"][0]
	uvClose(t, "pooled lo", ttCell(cl, 2, "LowerCLMean"), -8.1447442644833, 1e-9)
	uvClose(t, "satt hi", ttCell(cl, 3, "UpperCLMean"), 1.5128754535391726, 1e-9)
	eq := tb["Equality"][0]
	uvClose(t, "F", ttCell(eq, 0, "FValue"), 1.0328253945107975, 1e-10)
	uvClose(t, "Pr>F", ttCell(eq, 0, "ProbF"), 0.9526904129892558, 1e-9)
	one := tb["TTests"][1]
	uvClose(t, "t1", ttCell(one, 0, "tValue"), 1.9867191515302132, 1e-10)
	uvClose(t, "p1", ttCell(one, 0, "Probt"), 0.06239253072468521, 1e-9)
	c1 := tb["ConfLimits"][1]
	uvClose(t, "CL lo", ttCell(c1, 0, "LowerCLMean"), 59.86567093860419, 1e-10)
	uvClose(t, "SD lo", ttCell(c1, 0, "LowerCLStdDev"), 3.87408283, 1e-8)
	uvClose(t, "SD hi", ttCell(c1, 0, "UpperCLStdDev"), 7.58204467, 1e-8)
}
