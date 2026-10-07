package content

import (
	"my-bookmark/internal/summary"
	"strings"
	"testing"
)

func TestDefaultSummaryIsConcrete(t *testing.T) {
	if SummaryInstruction(summary.Standard) != SummaryInstruction(summary.Concrete) {
		t.Fatal("default summary must use concrete instructions")
	}
}
func TestTLDRRules(t *testing.T) {
	for _, style := range []summary.Style{summary.Standard, summary.Concrete, summary.Detailed, summary.Simple} {
		p := SummaryInstruction(style)
		if !strings.Contains(p, "原則3") || !strings.Contains(p, "tldr") {
			t.Fatalf("missing TLDR instruction: %q", style)
		}
	}
}

func TestValidateTLDR(t *testing.T) {
	for _, tc := range []struct {
		lines []string
		valid bool
	}{
		{[]string{"結論", "影響"}, true}, {[]string{"結論", "重要性", "影響"}, true},
		{[]string{"一", "二", "三", "四", "五"}, true},
		{nil, false}, {[]string{"一"}, false}, {[]string{"一", "二", "三", "四", "五", "六"}, false},
		{[]string{" ", "二"}, false}, {[]string{"一\n二", "三"}, false}, {[]string{"一", "一"}, false}, {[]string{strings.Repeat("長", 101), "二"}, false},
	} {
		if err := ValidateTLDR(tc.lines); (err == nil) != tc.valid {
			t.Fatalf("lines=%v err=%v", tc.lines, err)
		}
	}
}
