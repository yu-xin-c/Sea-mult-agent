package intent

import "testing"

func TestRewriteUserQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "帮我找一下 transformer 方面的 文献", want: "找 transformer 方面的 论文"},
		{in: "请问 有没有 图神经网络 的 paper？", want: "搜索 图神经网络 的 papers"},
		{in: "搜索 attention mechanism 论文", want: "搜索 attention mechanism 论文"},
	}

	for _, tc := range cases {
		got := RewriteUserQuery(tc.in)
		if got != tc.want {
			t.Fatalf("RewriteUserQuery(%q)=%q, want=%q", tc.in, got, tc.want)
		}
	}
}
