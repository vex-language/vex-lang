package types

import (
	"strings"
	"testing"
)

func TestWarnings(t *testing.T) {
	tests := []struct{ body, want string }{
		{"let x = 1", "3:5: 'x' is never used"},
		{"let _x = 1", ""},
		{"var n = 0\nn += 1", ""},
		{"let n = 1\nlet f = fn() { println(n) }\nf()", ""},
		{"let (a, b) = (1, 2)\nprintln(a)", "3:9: 'b' is never used"},
		{"let o: int? = 1\nif let v = o {\n}", "4:12: 'v' is never used"},
		{"return\nprintln(1)", "4:5: this code is never reached"},
		{"for i in 0..3 {\nbreak\nprintln(i)\n}", "5:5: this code is never reached"},
		{"while true {\n}\nprintln(1)", "5:5: this code is never reached"},
		{"panic(\"x\")\nprintln(1)\nprintln(2)", "4:5: this code is never reached"},
		{"if true {\nreturn\n}\nprintln(1)", ""},
	}
	for _, tt := range tests {
		info, errs := checkMain(t, "", strings.ReplaceAll(tt.body, "\n", "\n    "))
		if errs != nil {
			t.Errorf("%q: errors %v", tt.body, errs)
			continue
		}
		var got []string
		for _, w := range info.Warnings {
			if !w.Warning {
				t.Errorf("%q: %v isn't marked as a warning", tt.body, w)
			}
			got = append(got, w.Error())
		}
		if g := strings.Join(got, "\n"); g != tt.want {
			t.Errorf("%q:\n got %q\nwant %q", tt.body, g, tt.want)
		}
	}
}

func TestWarningsInGenericFunctions(t *testing.T) {
	info, errs := check(t, `fn largest<T: Comparable>(xs: [T]) -> T {
    var best = xs[0]
    let spare = 1
    for x in xs {
        if x > best {
            best = x
        }
    }
    return best
}

fn main() {
    println(largest([1, 3, 2]))
}
`)
	if errs != nil {
		t.Fatal(errs)
	}
	var got []string
	for _, w := range info.Warnings {
		got = append(got, w.Error())
	}
	if g := strings.Join(got, "\n"); g != "3:9: 'spare' is never used" {
		t.Errorf("got %q", g)
	}
}
