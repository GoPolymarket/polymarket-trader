package config

import (
	"os"
	"regexp"
	"testing"
)

func TestREADMEConfigDefaultsStayInSync(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	readme := string(data)

	assertDocDefault(t, readme, "maker.auto_select_top", "2")
	assertDocDefault(t, readme, "maker.order_size_usdc", "1")
	assertDocDefault(t, readme, "taker.amount_usdc", "1")
	assertDocDefault(t, readme, "risk.max_open_orders", "6")
	assertDocDefault(t, readme, "risk.max_position_per_market", "3")
}

func assertDocDefault(t *testing.T, readme, field, want string) {
	t.Helper()
	pattern := "\\| `" + regexp.QuoteMeta(field) + "` \\| [^\\n]*? \\| `([^`]+)` \\|"
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(readme)
	if len(m) != 2 {
		t.Fatalf("field %q not found in README config table", field)
	}
	if m[1] != want {
		t.Fatalf("README default mismatch for %s: want %s got %s", field, want, m[1])
	}
}
