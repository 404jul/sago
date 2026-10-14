package sas

import (
	"embed"
	"encoding/csv"
	"strings"
)

//go:embed sashelp/*.csv
var sashelpFS embed.FS

type helpVar struct {
	name   string
	char   bool
	length int
	format string
	label  string
}

var sashelpSchemas = map[string][]helpVar{
	"CLASS": {
		{"Name", true, 8, "", ""}, {"Sex", true, 1, "", ""}, {"Age", false, 8, "", ""},
		{"Height", false, 8, "", ""}, {"Weight", false, 8, "", ""},
	},
	"CARS": {
		{"Make", true, 13, "", ""}, {"Model", true, 40, "", ""}, {"Type", true, 8, "", ""},
		{"Origin", true, 6, "", ""}, {"DriveTrain", true, 5, "", ""},
		{"MSRP", false, 8, "DOLLAR8.", ""}, {"Invoice", false, 8, "DOLLAR8.", ""},
		{"EngineSize", false, 8, "", "Engine Size (L)"}, {"Cylinders", false, 8, "", ""},
		{"Horsepower", false, 8, "", ""}, {"MPG_City", false, 8, "", "MPG (City)"},
		{"MPG_Highway", false, 8, "", "MPG (Highway)"}, {"Weight", false, 8, "", "Weight (LBS)"},
		{"Wheelbase", false, 8, "", "Wheelbase (IN)"}, {"Length", false, 8, "", "Length (IN)"},
	},
	"IRIS": {
		{"Species", true, 10, "", "Iris Species"},
		{"SepalLength", false, 8, "", "Sepal Length (mm)"}, {"SepalWidth", false, 8, "", "Sepal Width (mm)"},
		{"PetalLength", false, 8, "", "Petal Length (mm)"}, {"PetalWidth", false, 8, "", "Petal Width (mm)"},
	},
	"HEART": {
		{"Status", true, 5, "", ""}, {"DeathCause", true, 26, "", "Cause of Death"},
		{"AgeCHDdiag", false, 8, "", "Age CHD Diagnosed"}, {"Sex", true, 6, "", ""},
		{"AgeAtStart", false, 8, "", "Age at Start"}, {"Height", false, 8, "", ""}, {"Weight", false, 8, "", ""},
		{"Diastolic", false, 8, "", ""}, {"Systolic", false, 8, "", ""},
		{"MRW", false, 8, "", "Metropolitan Relative Weight"}, {"Smoking", false, 8, "", ""},
		{"AgeAtDeath", false, 8, "", "Age at Death"}, {"Cholesterol", false, 8, "", ""},
		{"Chol_Status", true, 10, "", "Cholesterol Status"}, {"BP_Status", true, 7, "", "Blood Pressure Status"},
		{"Weight_Status", true, 11, "", "Weight Status"}, {"Smoking_Status", true, 17, "", "Smoking Status"},
	},
}

// SashelpNames lists the bundled SASHELP members.
func SashelpNames() []string { return []string{"CARS", "CLASS", "HEART", "IRIS"} }

func loadSashelp(member string) *Dataset {
	schema, ok := sashelpSchemas[member]
	if !ok {
		return nil
	}
	raw, err := sashelpFS.ReadFile("sashelp/" + strings.ToLower(member) + ".csv")
	if err != nil {
		return nil
	}
	recs, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(recs) < 1 {
		return nil
	}
	d := NewDataset("SASHELP." + member)
	for _, hv := range schema {
		d.Vars = append(d.Vars, &Var{Name: hv.name, Char: hv.char, Length: hv.length, Format: hv.format, Label: hv.label})
	}
	for _, rec := range recs[1:] {
		row := make([]Value, len(schema))
		for i, hv := range schema {
			f := ""
			if i < len(rec) {
				f = rec[i]
			}
			if hv.char {
				row[i] = StrV(f)
			} else {
				x, _ := parseNumber(f)
				row[i] = NumV(x)
			}
		}
		d.Rows = append(d.Rows, row)
	}
	return d
}
