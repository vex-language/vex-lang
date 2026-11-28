package codegen

import "testing"

func TestStructLayout(t *testing.T) {
	fields := cFields([]string{"int64_t f_x;", "vx_str f_name", "vx_list **c1;", "rec_L_int p0", "vx_fn f;", "double y", "opt_int o"})
	want := []cField{{"f_x", "int64_t "}, {"f_name", "vx_str "}, {"c1", "vx_list **"}, {"p0", "rec_L_int "}, {"f", "vx_fn "}, {"y", "double "}, {"o", "opt_int "}}
	if len(fields) != len(want) {
		t.Fatalf("got %v", fields)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Errorf("field %d: got %q, want %q", i, fields[i], want[i])
		}
	}
	got := structLayout("S_P", fields)
	wantLayout := "VX_LAYOUT(S_P, VX_FIELD(S_P, f_name, 1) | VX_FIELD(S_P, c1, 1) | VX_FIELD(S_P, p0, VXPM_rec_L_int) | VX_FIELD(S_P, f, VX_PM_FN) | VX_FIELD(S_P, o, VXPM_opt_int))"
	if got != wantLayout {
		t.Errorf("got %s\nwant %s", got, wantLayout)
	}
	if got := structLayout("S_N", cFields([]string{"int64_t a", "double b", "bool c", "vx_range r"})); got != "0" {
		t.Errorf("a struct without pointers: got %s", got)
	}
	if got := cLayout("vx_map_iter"); got != "VX_ONES(sizeof(vx_map_iter))" {
		t.Errorf("an unknown type: got %s", got)
	}
}
