package ids

import "testing"

func TestNewIsPrefixedAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 256; i++ {
		id := New(Run)
		if !Valid(Run, id) {
			t.Fatalf("invalid id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestValidRejectsMalformedValues(t *testing.T) {
	good := New(Project)
	for _, bad := range []string{"", "prj", "prj_", "run_" + good[len("prj_"):], good + "0", good[:len(good)-1], "prj_" + "G" + good[len("prj_")+1:], "prj_" + "A" + good[len("prj_")+1:]} {
		if Valid(Project, bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
	if !Valid(Project, good) {
		t.Fatal("rejected a valid id")
	}
}

func TestTokenLength(t *testing.T) {
	if got := len(Token(20)); got != 40 {
		t.Fatalf("token length = %d", got)
	}
}
