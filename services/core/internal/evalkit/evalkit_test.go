package evalkit

import "testing"

func TestMatches(t *testing.T) {
	cases := []struct {
		e, g string
		want bool
	}{
		{"Spark", "Apache Spark", true},
		{"Spring Boot", "spring boot", true},
		{"Go", "Golang", false},
		{"Go", "Go", true},
		{"C#", "c#", true},
		{"Java", "JavaScript", false},
		{"IBM MQ", "IBM MQ", true},
	}
	for _, c := range cases {
		if got := Matches(c.e, c.g); got != c.want {
			t.Errorf("Matches(%q, %q) = %v", c.e, c.g, got)
		}
	}
}

func TestRecall(t *testing.T) {
	r, missing := Recall([]string{"Go", "Kafka", "Redis"}, []string{"go", "Apache Kafka"})
	if r < 0.66 || r > 0.67 || len(missing) != 1 || missing[0] != "Redis" {
		t.Fatalf("recall=%v missing=%v", r, missing)
	}
}

func TestGroundedRate(t *testing.T) {
	r, bad := GroundedRate("Skills: Go, Kafka and PostgreSQL.", []string{"Go", "Kafka", "Rust"})
	if r < 0.66 || r > 0.67 || bad[0] != "Rust" {
		t.Fatalf("rate=%v bad=%v", r, bad)
	}
}

func TestLoadGolden(t *testing.T) {
	g, err := Load("../../../../testdata/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Fixtures) != 3 {
		t.Fatalf("fixtures = %d", len(g.Fixtures))
	}
	for _, f := range g.Fixtures {
		src, err := g.SourceText(f)
		if err != nil {
			t.Fatal(err)
		}
		// Golden answers must themselves be grounded in the fixture text.
		if r, bad := GroundedRate(src, f.Skills); r != 1 {
			t.Errorf("%s: golden skills not in source: %v", f.ID, bad)
		}
		if r, bad := GroundedRate(src, f.Companies); r != 1 {
			t.Errorf("%s: golden companies not in source: %v", f.ID, bad)
		}
	}
}
