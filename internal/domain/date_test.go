package domain

import (
	"encoding/json"
	"testing"
)

func TestParseDate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		ok   bool
		want string
	}{
		{"2026-06-01", true, "2026-06-01"},
		{"2026-2-1", false, ""},
		{"hier", false, ""},
		{"", false, ""},
	} {
		d, err := ParseDate(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseDate(%q): err=%v, ok attendu=%v", tc.in, err, tc.ok)
		}
		if tc.ok && d.String() != tc.want {
			t.Errorf("ParseDate(%q) = %s, attendu %s", tc.in, d, tc.want)
		}
	}
}

func TestDateOrdering(t *testing.T) {
	a, _ := ParseDate("2026-01-31")
	b, _ := ParseDate("2026-02-01")
	if !a.Before(b) || b.Before(a) || a.Before(a) {
		t.Errorf("ordre incorrect entre %s et %s", a, b)
	}
}

func TestDateJSON(t *testing.T) {
	d, _ := ParseDate("2026-06-01")
	raw, err := json.Marshal(struct{ D Date }{d})
	if err != nil || string(raw) != `{"D":"2026-06-01"}` {
		t.Fatalf("marshal = %s, err=%v", raw, err)
	}
	var back struct{ D Date }
	if err := json.Unmarshal(raw, &back); err != nil || back.D != d {
		t.Fatalf("unmarshal = %+v, err=%v", back.D, err)
	}
}

func TestDateAsJSONMapKey(t *testing.T) {
	d, _ := ParseDate("2026-06-01")
	raw, err := json.Marshal(map[Date]int{d: 1})
	if err != nil || string(raw) != `{"2026-06-01":1}` {
		t.Fatalf("marshal = %s, err=%v", raw, err)
	}
	var back map[Date]int
	if err := json.Unmarshal(raw, &back); err != nil || back[d] != 1 {
		t.Fatalf("unmarshal = %v, err=%v", back, err)
	}
}

func TestDateUnmarshalRejectsGarbage(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte(`"pas-une-date"`), &d); err == nil {
		t.Fatal("unmarshal aurait dû échouer")
	}
}

func TestDateAddDays(t *testing.T) {
	dd, _ := ParseDate("2026-06-01")
	if got := dd.AddDays(-7).String(); got != "2026-05-25" {
		t.Errorf("AddDays(-7) = %s", got)
	}
	if got := dd.AddDays(30).String(); got != "2026-07-01" {
		t.Errorf("AddDays(30) = %s", got)
	}
}
