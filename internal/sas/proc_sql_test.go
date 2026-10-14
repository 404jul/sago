package sas

import (
	"math"
	"testing"
)

// PROC SQL pinned against pandas group-by semantics.
func sqlProgDS(t *testing.T, prog string) *Dataset {
	t.Helper()
	s := NewSession(t.TempDir())
	s.RunProgram(prog)
	if s.Log.Errors > 0 {
		t.Fatalf("errors: %v", s.Log.Entries)
	}
	return s.Dataset("_LAST_")
}

func TestSqlGroupByAggregates(t *testing.T) {
	d := sqlProgDS(t, `proc sql noprint;
	  create table res as
	  select Sex, count(*) as n, mean(Height) as mh format=best12., std(Weight) as sw
	  from sashelp.class group by Sex;
	quit;`)
	// pandas: class grouped by Sex: F n=9 mh=60.588888..., M n=10; sw F=17.3663, M=22.7272
	want := map[string][4]float64{
		"F": {9, 60.588888888888886, 19.383913720173002, 0},
		"M": {10, 63.91, 22.72718636347227, 0},
	}
	for _, r := range d.Rows {
		w := want[r[0].Str]
		if math.Abs(r[1].Num-w[0]) > 1e-9 || math.Abs(r[2].Num-w[1]) > 1e-9 || math.Abs(r[3].Num-w[2]) > 1e-9 {
			t.Errorf("row %v: got %v %v %v", r[0].Str, r[1].Num, r[2].Num, r[3].Num)
		}
	}
}

func TestSqlBooleanAggAndWhere(t *testing.T) {
	// Project1 form: MEAN(Prob < 0.05)
	d := sqlProgDS(t, `proc sql noprint;
	  create table t1 as
	  select Statistic, MEAN(Prob < 0.05) AS ErrorRate
	  from (select 'Chi-Square' as Statistic, 0.01 as Prob from sashelp.class (obs=6)
	        union all
	        select 'Chi-Square' as Statistic, 0.50 as Prob from sashelp.class (obs=4))
	  group by Statistic;
	quit;`)
	if len(d.Rows) != 1 || math.Abs(d.Rows[0][1].Num-0.6) > 1e-12 {
		t.Errorf("ErrorRate = %v, want 0.6", d.Rows[0][1].Num)
	}
}

func TestSqlJoinDistinct(t *testing.T) {
	d := sqlProgDS(t, `proc sql noprint;
	  create table j as
	  select a.Name, b.Weight
	  from sashelp.class(obs=3) as a
	  inner join sashelp.class as b
	  on a.Name = b.Name
	  order by b.Weight desc;
	quit;`)
	if len(d.Rows) != 3 || d.Rows[0][1].Num != 112.5 {
		t.Errorf("join rows wrong: %v", d.Rows)
	}
	d2 := sqlProgDS(t, `proc sql noprint;
	  create table u as select distinct Age from sashelp.class order by Age;
	quit;`)
	if len(d2.Rows) != 6 || d2.Rows[0][0].Num != 11 {
		t.Errorf("distinct ages wrong: %d rows", len(d2.Rows))
	}
}
