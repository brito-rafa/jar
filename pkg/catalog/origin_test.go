package catalog

import "testing"

func TestFilenameMetadata(t *testing.T) {
	for _, tc := range []struct{ label, date, precision string }{
		{"Conferencias/580508RN.DOC | section 1", "1958-05-08", "day"},
		{"Conferencias/320600CF.DOC | section 1", "1932-06", "month"},
		{"Conferencias/3100-1CF.DOC | section 1", "1931", "year"},
		{"Conferencias/580230CA.DOC | section 1", "", ""},
		{"Books/Edition_19300811.pdf | page 2", "1930-08-11", "day"},
		{"Books/La_Herencia_de_Sí_Mismo.pdf | page 3", "", ""},
	} {
		o := Parse(tc.label)
		if o.Date != tc.date || o.Precision != tc.precision {
			t.Fatalf("%s: %+v", tc.label, o)
		}
	}
	if Normalize("La Herencia de Sí Mismo") != "la herencia de si mismo" {
		t.Fatal("accent-insensitive title normalization failed")
	}
	for _, bad := range []string{"1958-02-30", "58-05-08", "1958-13", "1958; DROP TABLE items"} {
		if ValidateDate(bad) == nil {
			t.Fatal(bad)
		}
	}
}
