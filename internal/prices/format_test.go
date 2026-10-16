package prices_test

import (
	"math/big"
	"testing"

	"github.com/JustAzul/cc-msgate/internal/prices"
)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return r
}

func TestFormatUSD(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0.000000"},
		{"0.68", "0.680000"},
		{"0.952", "0.952000"},
		{"52.940726", "52.940726"},
		{"1/2000000", "0.000000"}, // 0.0000005: half to even rounds down to 0
		{"3/2000000", "0.000002"}, // 0.0000015: half to even rounds up to 2
		{"5/2000000", "0.000002"}, // 0.0000025: half to even stays at 2
		{"7/2000000", "0.000004"}, // 0.0000035: half to even rounds up to 4
		{"0.0000006", "0.000001"},
		{"0.0000004", "0.000000"},
		{"-0.0000015", "-0.000002"},
		{"-0.0000004", "0.000000"},
	}
	for _, c := range cases {
		if got := prices.FormatUSD(rat(t, c.in)); got != c.want {
			t.Errorf("FormatUSD(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatCents(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.952", "0.95"},
		{"0.012", "0.01"},
		{"1.005", "1.01"},
		{"0.005", "0.01"},
		{"0.004999", "0.00"},
		{"52.94", "52.94"},
		{"0", "0.00"},
		{"100", "100.00"},
	}
	for _, c := range cases {
		if got := prices.FormatCents(rat(t, c.in)); got != c.want {
			t.Errorf("FormatCents(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatDecimal(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2", "2"},
		{"0", "0"},
		{"1/10", "0.1"},
		{"5/2", "2.5"},
		{"0.125", "0.125"},
		{"30", "30"},
		{"1/1000000", "0.000001"},
		{"-1/4", "-0.25"},
	}
	for _, c := range cases {
		got, err := prices.FormatDecimal(rat(t, c.in))
		if err != nil || got != c.want {
			t.Errorf("FormatDecimal(%s) = %q, %v, want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"1/3", "1/7", "2/3"} {
		if got, err := prices.FormatDecimal(rat(t, in)); err == nil {
			t.Errorf("FormatDecimal(%s) = %q, want an error", in, got)
		}
	}
}
