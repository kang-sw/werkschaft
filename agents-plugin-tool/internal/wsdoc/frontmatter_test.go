package wsdoc

import "testing"

func TestCleanScalarAndFrontmatterTitleHashInQuotes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "double-quoted Korean title with #N",
			raw:  ` "컴포저 긴 붙여넣기 자동 접기(#N 텍스트 참조 배지)"`,
			want: "컴포저 긴 붙여넣기 자동 접기(#N 텍스트 참조 배지)",
		},
		{
			name: "double-quoted with space-hash inside",
			raw:  ` "foo (#1 text ref) #2"`,
			want: "foo (#1 text ref) #2",
		},
		{
			name: "double-quoted with trailing comment",
			raw:  ` "foo (#1 text ref) #2" # trailing comment`,
			want: "foo (#1 text ref) #2",
		},
		{
			name: "double-quoted escaped quote",
			raw:  ` "say \"#1\" ok"`,
			want: `say "#1" ok`,
		},
		{
			name: "single-quoted with space-hash inside",
			raw:  ` 'foo (#1 text ref) #2'`,
			want: "foo (#1 text ref) #2",
		},
		{
			name: "single-quoted with doubled quote and trailing comment",
			raw:  ` 'it''s #1 ref' # trailing comment`,
			want: "it's #1 ref",
		},
		{
			name: "unquoted strips inline comment",
			raw:  ` foo bar # real comment`,
			want: "foo bar",
		},
		{
			name: "unquoted keeps hash without leading space",
			raw:  ` foo#bar`,
			want: "foo#bar",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanScalar(tc.raw); got != tc.want {
				t.Fatalf("cleanScalar(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			doc := "---\ntitle:" + tc.raw + "\n---\n# Body\n"
			fm := frontmatterFromText(doc)
			if got, _ := fm["title"].(string); got != tc.want {
				t.Fatalf("frontmatterFromText title = %q, want %q", got, tc.want)
			}
		})
	}
}
