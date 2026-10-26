package check

import "testing"

func TestAnswer(t *testing.T) {
	tests := []struct {
		given    string
		accepted []string
		english  bool
		want     Verdict
	}{
		{"pidempi", []string{"pidempi", "pitempi"}, false, Correct},
		{"Pitempi.", []string{"pidempi", "pitempi"}, false, Correct},
		{"  Tämä on paras ravintola ", []string{"Tämä on paras ravintola."}, false, Correct},
		{"tama on paras ravintola", []string{"Tämä on paras ravintola."}, false, Close},
		{"isossa kaupungisa", []string{"isossa kaupungissa"}, false, Close},
		{"isompi", []string{"pidempi"}, false, Wrong},
		{"", []string{"pidempi"}, false, Wrong},
		{"reliable", []string{"reliable", "trustworthy"}, true, Correct},
		{"to apply", []string{"apply"}, true, Correct},
		{"apply", []string{"to apply"}, true, Correct},
		{"take care", []string{"(to) take care of"}, true, Wrong}, // missing "of" is more than a typo
		{"take care of", []string{"(to) take care of"}, true, Correct},
		{"ja", []string{"jo"}, false, Wrong}, // short words get no typo tolerance
		{"espanol", []string{"español"}, false, Close},
		{"cafe creme", []string{"café crème"}, false, Close},
		{"strasse", []string{"straße"}, false, Close},
		{"café", []string{"café"}, false, Correct},
	}
	for _, tt := range tests {
		if got := Answer(tt.given, tt.accepted, tt.english); got.Verdict != tt.want {
			t.Errorf("Answer(%q, %q) = %s (%s), want %s", tt.given, tt.accepted, got.Verdict, got.Reason, tt.want)
		}
	}
}
