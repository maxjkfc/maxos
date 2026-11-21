package svgchart_test

import (
	"encoding/xml"
	"errors"
	"html/template"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/pkg/svgchart"
)

// node is a parsed XML element.
type node struct {
	name  string
	attrs map[string]string
	text  string // the element's own character data
	kids  []*node
}

// parse decodes h as exactly one well-formed <svg> element.
func parse(t *testing.T, h template.HTML) *node {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(string(h)))
	dec.Strict = true
	var root *node
	var stack []*node
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("output is not well-formed XML: %v\n%s", err, h)
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			n := &node{name: tk.Name.Local, attrs: map[string]string{}}
			for _, a := range tk.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			if len(stack) == 0 {
				if root != nil {
					t.Fatalf("more than one root element\n%s", h)
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.kids = append(parent.kids, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(tk)) != "" {
					t.Fatalf("text outside the root element\n%s", h)
				}
				continue
			}
			stack[len(stack)-1].text += string(tk)
		}
	}
	if root == nil || root.name != "svg" {
		t.Fatalf("no <svg> root in %q", h)
	}
	return root
}

// walk calls fn for n and every descendant, in document order.
func (n *node) walk(fn func(*node)) {
	fn(n)
	for _, k := range n.kids {
		k.walk(fn)
	}
}

// byName returns the descendants named name.
func (n *node) byName(name string) []*node {
	var out []*node
	n.walk(func(m *node) {
		if m != n && m.name == name {
			out = append(out, m)
		}
	})
	return out
}

// hasClass reports whether the class attribute holds the token class.
func (n *node) hasClass(class string) bool {
	for _, c := range strings.Fields(n.attrs["class"]) {
		if c == class {
			return true
		}
	}
	return false
}

// byClass returns the descendants carrying the class token.
func (n *node) byClass(class string) []*node {
	var out []*node
	n.walk(func(m *node) {
		if m != n && m.hasClass(class) {
			out = append(out, m)
		}
	})
	return out
}

func attr(nodes []*node, name string) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.attrs[name]
	}
	return out
}

func texts(nodes []*node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.text
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// vals builds plain bars (labels "b0", "b1", ...) from decimal strings.
func vals(values ...string) []svgchart.Bar {
	bars := make([]svgchart.Bar, len(values))
	for i, v := range values {
		bars[i] = svgchart.Bar{Label: "b" + string(rune('0'+i)), Value: d(v), Text: v}
	}
	return bars
}

var chart = svgchart.Chart{ID: "c1", Title: "Spending"}

// corpus is every shape of input the renderers must survive.
func corpus() map[string][]svgchart.Bar {
	hostile := `<script>alert("x")</script> & 'q' "dq"`
	return map[string][]svgchart.Bar{
		"zeros":           vals("0", "0", "0"),
		"one":             vals("42"),
		"equal":           vals("5", "5", "5", "5"),
		"huge+small":      vals("1000000000", "1"),
		"tiny+huge":       vals("0.000000001", "1000000000000000000000000000000"),
		"negative":        vals("-5", "-1"),
		"mixed":           vals("10", "-3", "0", "7.77"),
		"twelve":          vals("1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"),
		"thirds":          vals("1", "2", "3"),
		"hostile":         {{Label: hostile, Value: d("3"), Text: hostile, Href: `/x?a="><script>&'`, Highlight: true}},
		"bad hrefs":       {{Label: "a", Value: d("1"), Href: "javascript:alert(1)"}, {Label: "b", Value: d("1"), Href: "//evil"}},
		"cjk":             {{Label: "飲食", Value: d("1200"), Text: "1,200.00 TWD"}, {Label: "交通", Value: d("300"), Text: "300.00 TWD", Highlight: true}},
		"control":         {{Label: "a\x00b\x01c\xffd", Value: d("1"), Text: "\x1b[31m", Highlight: true}},
		"long":            {{Label: strings.Repeat("長", 5000), Value: d("1"), Text: strings.Repeat("9", 5000), Highlight: true}},
		"many":            manyBars(200),
		"all highlighted": {{Label: "a", Value: d("1"), Highlight: true}, {Label: "b", Value: d("2"), Highlight: true}},
	}
}

func manyBars(n int) []svgchart.Bar {
	bars := make([]svgchart.Bar, n)
	for i := range bars {
		bars[i] = svgchart.Bar{Label: "m", Value: decimal.NewFromInt(int64(i)), Highlight: i%7 == 0}
	}
	return bars
}

type renderer struct {
	name string
	fn   func(svgchart.Chart, []svgchart.Bar) template.HTML
}

var renderers = []renderer{
	{"HBars", svgchart.HBars},
	{"Columns", svgchart.Columns},
}

func TestHBarsLengths(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   []string // widths of the bar rects, in bar order, zero bars skipped
	}{
		{"zeros draw no bar", []string{"0", "0"}, nil},
		{"negatives draw no bar", []string{"-5", "-1"}, nil},
		{"one bar is full length", []string{"7"}, []string{"240"}},
		{"equal bars are all full length", []string{"3", "3", "3"}, []string{"240", "240", "240"}},
		{"proportional", []string{"100", "50", "25"}, []string{"240", "120", "60"}},
		{"zero in the middle has no rect", []string{"10", "0", "5"}, []string{"240", "120"}},
		{"negative is drawn as zero", []string{"10", "-3"}, []string{"240"}},
		{"rounds to one decimal", []string{"7", "1"}, []string{"240", "34.3"}},
		{"small next to huge keeps the minimum", []string{"1000000000", "1"}, []string{"240", "2"}},
		{"unit order does not matter", []string{"1", "1000000000"}, []string{"2", "240"}},
		{"fractions", []string{"0.3", "0.1"}, []string{"240", "80"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parse(t, svgchart.HBars(chart, vals(tt.values...)))
			got := attr(root.byClass(svgchart.ClassBar), "width")
			if !equal(got, tt.want) {
				t.Errorf("bar widths = %v, want %v", got, tt.want)
			}
			if labels := root.byClass(svgchart.ClassLabel); len(labels) != len(tt.values) {
				t.Errorf("%d labels drawn, want %d: labels stay even without bars", len(labels), len(tt.values))
			}
			if tracks := root.byClass(svgchart.ClassTrack); len(tracks) != len(tt.values) {
				t.Errorf("%d tracks, want %d", len(tracks), len(tt.values))
			}
		})
	}
}

func TestColumnsHeights(t *testing.T) {
	tests := []struct {
		name       string
		values     []string
		wantHeight []string
		wantY      []string
	}{
		{"zeros", []string{"0", "0"}, nil, nil},
		{"negatives", []string{"-2", "-9"}, nil, nil},
		{"one column is full height", []string{"9"}, []string{"104"}, []string{"36"}},
		{"equal columns", []string{"4", "4"}, []string{"104", "104"}, []string{"36", "36"}},
		{"proportional", []string{"100", "50", "0"}, []string{"104", "52"}, []string{"36", "88"}},
		{"small next to huge keeps the minimum", []string{"1000000000", "1"}, []string{"104", "2"}, []string{"36", "138"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parse(t, svgchart.Columns(chart, vals(tt.values...)))
			rects := root.byClass(svgchart.ClassBar)
			if got := attr(rects, "height"); !equal(got, tt.wantHeight) {
				t.Errorf("heights = %v, want %v", got, tt.wantHeight)
			}
			if got := attr(rects, "y"); !equal(got, tt.wantY) {
				t.Errorf("tops = %v, want %v", got, tt.wantY)
			}
			if labels := root.byClass(svgchart.ClassLabel); len(labels) != len(tt.values) {
				t.Errorf("%d labels, want one under every column (%d)", len(labels), len(tt.values))
			}
		})
	}
}

func TestEmptyData(t *testing.T) {
	for _, r := range renderers {
		if got := r.fn(chart, nil); got != "" {
			t.Errorf("%s(nil) = %q, want empty", r.name, got)
		}
		if got := r.fn(chart, []svgchart.Bar{}); got != "" {
			t.Errorf("%s(empty) = %q, want empty", r.name, got)
		}
	}
}

func TestOrderIsCallers(t *testing.T) {
	bars := []svgchart.Bar{
		{Label: "c", Value: d("1")},
		{Label: "a", Value: d("30")},
		{Label: "b", Value: d("2")},
		{Label: "飲食", Value: d("0")},
	}
	for _, r := range renderers {
		t.Run(r.name, func(t *testing.T) {
			root := parse(t, r.fn(chart, bars))
			got := texts(root.byClass(svgchart.ClassLabel))
			if want := []string{"c", "a", "b", "飲食"}; !equal(got, want) {
				t.Errorf("labels = %v, want %v", got, want)
			}
		})
	}
}

func TestHighlight(t *testing.T) {
	bars := []svgchart.Bar{
		{Label: "a", Value: d("1")},
		{Label: "b", Value: d("2"), Highlight: true},
		{Label: "c", Value: d("3")},
		{Label: "d", Value: d("4"), Highlight: true},
	}
	for _, r := range renderers {
		t.Run(r.name, func(t *testing.T) {
			root := parse(t, r.fn(chart, bars))
			rects := root.byClass(svgchart.ClassBar)
			if len(rects) != 4 {
				t.Fatalf("%d bars, want 4", len(rects))
			}
			var got []bool
			for _, rc := range rects {
				got = append(got, rc.hasClass(svgchart.ClassBarHighlight))
			}
			want := []bool{false, true, false, true}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("highlight flags = %v, want %v", got, want)
					break
				}
			}
			if hl := root.byClass(svgchart.ClassBarHighlight); len(hl) != 2 {
				t.Errorf("%d elements with %s, want 2", len(hl), svgchart.ClassBarHighlight)
			}
		})
	}
}

func TestColumnsTextOnlyForHighlighted(t *testing.T) {
	bars := []svgchart.Bar{
		{Label: "1月", Value: d("10"), Text: "10 TWD"},
		{Label: "2月", Value: d("20"), Text: "20 TWD", Highlight: true},
		{Label: "3月", Value: d("30"), Text: "30 TWD"},
	}
	root := parse(t, svgchart.Columns(chart, bars))
	if got, want := texts(root.byClass(svgchart.ClassValue)), []string{"20 TWD"}; !equal(got, want) {
		t.Errorf("value texts = %v, want %v", got, want)
	}

	none := parse(t, svgchart.Columns(chart, vals("1", "2")))
	if got := none.byClass(svgchart.ClassValue); len(got) != 0 {
		t.Errorf("no highlight but %d value texts drawn", len(got))
	}

	// A zero highlighted column still shows its Text, sitting on the baseline.
	zero := parse(t, svgchart.Columns(chart, []svgchart.Bar{{Label: "a", Value: d("0"), Text: "0 TWD", Highlight: true}, {Label: "b", Value: d("5")}}))
	if got := texts(zero.byClass(svgchart.ClassValue)); !equal(got, []string{"0 TWD"}) {
		t.Errorf("zero highlighted column text = %v", got)
	}
}

func TestHBarsTextOnEveryRow(t *testing.T) {
	bars := []svgchart.Bar{
		{Label: "飲食", Value: d("3"), Text: "3 TWD"},
		{Label: "交通", Value: d("1"), Text: ""},
		{Label: "x", Value: d("2"), Text: "2 TWD", Highlight: true},
	}
	root := parse(t, svgchart.HBars(chart, bars))
	if got, want := texts(root.byClass(svgchart.ClassValue)), []string{"3 TWD", "2 TWD"}; !equal(got, want) {
		t.Errorf("value texts = %v, want %v", got, want)
	}
	for _, v := range root.byClass(svgchart.ClassValue) {
		if v.attrs["x"] != "360" || v.attrs["text-anchor"] != "end" {
			t.Errorf("value text %q must be right-aligned at the far right, got x=%s anchor=%s", v.text, v.attrs["x"], v.attrs["text-anchor"])
		}
	}
	for _, l := range root.byClass(svgchart.ClassLabel) {
		if l.attrs["x"] != "0" || l.attrs["text-anchor"] != "start" {
			t.Errorf("label %q must be left-aligned, got x=%s anchor=%s", l.text, l.attrs["x"], l.attrs["text-anchor"])
		}
	}
	if got := root.attrs["viewBox"]; got != "0 0 360 116" {
		t.Errorf("viewBox = %q, want 0 0 360 116 (3 rows of 36 plus padding)", got)
	}
}

func TestColumnsStaysInsideViewBox(t *testing.T) {
	for _, n := range []int{1, 2, 3, 12, 31} {
		for _, hl := range []int{0, n / 2, n - 1} {
			bars := make([]svgchart.Bar, n)
			for i := range bars {
				bars[i] = svgchart.Bar{Label: "m", Value: decimal.NewFromInt(int64(i + 1)), Text: "123,456.00 TWD", Highlight: i == hl}
			}
			root := parse(t, svgchart.Columns(chart, bars))
			vb := strings.Fields(root.attrs["viewBox"])
			if len(vb) != 4 || vb[2] != "360" || vb[3] != "170" {
				t.Fatalf("viewBox = %q", root.attrs["viewBox"])
			}
			for _, r := range root.byClass(svgchart.ClassBar) {
				x, w := d(r.attrs["x"]), d(r.attrs["width"])
				y, h := d(r.attrs["y"]), d(r.attrs["height"])
				if x.Sign() < 0 || x.Add(w).GreaterThan(d("360")) || y.Sign() < 0 || y.Add(h).GreaterThan(d("170")) {
					t.Errorf("n=%d: column x=%s w=%s y=%s h=%s leaves the viewBox", n, x, w, y, h)
				}
			}
			values := root.byClass(svgchart.ClassValue)
			if len(values) != 1 {
				t.Fatalf("n=%d hl=%d: %d value texts, want 1", n, hl, len(values))
			}
			v := values[0]
			x := d(v.attrs["x"])
			// "123,456.00 TWD" is 14 characters; at most about 8 units each.
			const textWidth = 112
			switch v.attrs["text-anchor"] {
			case "start":
				if x.Sign() < 0 || x.Add(d("112")).GreaterThan(d("360")) {
					t.Errorf("n=%d hl=%d: start-anchored text at %s runs out of the viewBox", n, hl, x)
				}
			case "end":
				if x.GreaterThan(d("360")) || x.Sub(d("112")).Sign() < 0 {
					t.Errorf("n=%d hl=%d: end-anchored text at %s runs out of the viewBox", n, hl, x)
				}
			case "middle":
				if x.Sub(d("56")).Sign() < 0 || x.Add(d("56")).GreaterThan(d("360")) {
					t.Errorf("n=%d hl=%d: centred text at %s runs out of the viewBox (width %d)", n, hl, x, textWidth)
				}
			default:
				t.Errorf("unexpected anchor %q", v.attrs["text-anchor"])
			}
			if n >= 12 && hl == 0 && v.attrs["text-anchor"] != "start" {
				t.Errorf("first of %d columns: anchor %q, want start", n, v.attrs["text-anchor"])
			}
			if n >= 12 && hl == n-1 && v.attrs["text-anchor"] != "end" {
				t.Errorf("last of %d columns: anchor %q, want end", n, v.attrs["text-anchor"])
			}
		}
	}
}

func TestHrefFiltering(t *testing.T) {
	tests := []struct {
		href string
		want string // "" = dropped
	}{
		{"/budget", "/budget"},
		{"/", "/"},
		{"/months/2026-11?cat=a&b=c", "/months/2026-11?cat=a&b=c"},
		{"/a/b/c#frag", "/a/b/c#frag"},
		{"", ""},
		{"//evil.example/x", ""},
		{"///evil", ""},
		{"javascript:alert(1)", ""},
		{"JavaScript:alert(1)", ""},
		{"http://evil.example", ""},
		{"https://evil.example", ""},
		{"data:text/html,x", ""},
		{"relative/path", ""},
		{`/\evil.example`, ""},
		{"/\t/evil.example", ""},
		{"/\n/evil.example", ""},
		{" /x", ""},
		{"/x y", ""},
	}
	for _, r := range renderers {
		for _, tt := range tests {
			t.Run(r.name+"/"+tt.href, func(t *testing.T) {
				root := parse(t, r.fn(chart, []svgchart.Bar{{Label: "L", Value: d("5"), Text: "5", Href: tt.href, Highlight: true}}))
				anchors := root.byName("a")
				if tt.want == "" {
					if len(anchors) != 0 {
						t.Errorf("href %q produced a link to %q", tt.href, anchors[0].attrs["href"])
					}
				} else {
					if len(anchors) != 1 || anchors[0].attrs["href"] != tt.want {
						t.Fatalf("href %q: anchors = %v, want one link to %q", tt.href, attr(anchors, "href"), tt.want)
					}
					if len(anchors[0].byClass(svgchart.ClassBar)) != 1 || len(anchors[0].byClass(svgchart.ClassLabel)) != 1 {
						t.Errorf("the link must wrap the bar and its label")
					}
				}
				if got := root.byClass(svgchart.ClassBar); len(got) != 1 {
					t.Errorf("the bar must be drawn with or without a link, got %d", len(got))
				}
			})
		}
	}
}

func TestEscaping(t *testing.T) {
	const hostile = `<script>alert("x")</script> & 'q' "dq" <b>`
	bar := svgchart.Bar{Label: hostile, Value: d("3"), Text: hostile, Href: `/p?a="><script>&b='`, Highlight: true}
	c := svgchart.Chart{ID: `x"><script>`, Title: hostile}
	for _, r := range renderers {
		t.Run(r.name, func(t *testing.T) {
			out := string(r.fn(c, []svgchart.Bar{bar}))
			if strings.Contains(out, "<script") || strings.Contains(out, "<b>") {
				t.Fatalf("raw markup in output: %s", out)
			}
			if !strings.Contains(out, "&lt;script&gt;") {
				t.Errorf("<script> must come out as &lt;script&gt;: %s", out)
			}
			root := parse(t, template.HTML(out))
			// Escaping must round-trip: the parsed text is what the caller sent.
			if got := texts(root.byClass(svgchart.ClassLabel)); !equal(got, []string{hostile}) {
				t.Errorf("label round trip = %q", got)
			}
			if got := texts(root.byName("title")); !equal(got, []string{hostile}) {
				t.Errorf("title round trip = %q", got)
			}
			if got := texts(root.byName("desc")); !equal(got, []string{hostile + ": " + hostile}) {
				t.Errorf("desc round trip = %q", got)
			}
			if got := attr(root.byName("a"), "href"); !equal(got, []string{`/p?a="><script>&b='`}) {
				t.Errorf("href round trip = %q", got)
			}
			if id := root.byName("title")[0].attrs["id"]; id != "x---script--title" {
				t.Errorf("title id = %q", id)
			}
		})
	}
	// Progress takes caller text only through the Title.
	out := svgchart.Progress(c, d("1"), d("2"))
	if strings.Contains(string(out), "<script") || !strings.Contains(string(out), "&lt;script&gt;") {
		t.Errorf("Progress did not escape its title: %s", out)
	}
	parse(t, out)
}

func TestEscapedQuotesStayInsideAttributes(t *testing.T) {
	out := string(svgchart.HBars(svgchart.Chart{ID: "i", Title: `t" onload="alert(1)`}, []svgchart.Bar{{Label: "l", Value: d("1"), Href: `/x" onclick="alert(1)`}}))
	root := parse(t, template.HTML(out))
	root.walk(func(n *node) {
		for name := range n.attrs {
			if strings.HasPrefix(name, "on") {
				t.Errorf("event handler attribute %q on <%s>", name, n.name)
			}
		}
	})
}

func TestIDSanitising(t *testing.T) {
	tests := []struct{ id, want string }{
		{"", "chart"},
		{"spend-1_a", "spend-1_a"},
		{"Cat.Month 2026/11", "Cat-Month-2026-11"},
		{"飲食", "--"},
		{`x"><script>`, "x---script-"},
		{"a b", "a-b"},
	}
	for _, tt := range tests {
		for _, r := range renderers {
			t.Run(r.name+"/"+tt.id, func(t *testing.T) {
				root := parse(t, r.fn(svgchart.Chart{ID: tt.id, Title: "T"}, vals("1")))
				if got, want := root.attrs["aria-labelledby"], tt.want+"-title "+tt.want+"-desc"; got != want {
					t.Errorf("aria-labelledby = %q, want %q", got, want)
				}
				if got := root.byName("title")[0].attrs["id"]; got != tt.want+"-title" {
					t.Errorf("title id = %q", got)
				}
				if got := root.byName("desc")[0].attrs["id"]; got != tt.want+"-desc" {
					t.Errorf("desc id = %q", got)
				}
			})
		}
		t.Run("Progress/"+tt.id, func(t *testing.T) {
			root := parse(t, svgchart.Progress(svgchart.Chart{ID: tt.id, Title: "T"}, d("1"), d("2")))
			if got, want := root.attrs["aria-labelledby"], tt.want+"-title "+tt.want+"-desc"; got != want {
				t.Errorf("aria-labelledby = %q, want %q", got, want)
			}
		})
	}
}

func TestRootAndDescription(t *testing.T) {
	bars := []svgchart.Bar{
		{Label: "飲食", Value: d("1200"), Text: "1,200.00 TWD"},
		{Label: "交通", Value: d("300"), Text: "300.00 TWD"},
		{Label: "其他", Value: d("0")},
	}
	for _, r := range renderers {
		t.Run(r.name, func(t *testing.T) {
			out := r.fn(svgchart.Chart{ID: "cat", Title: "各類別支出"}, bars)
			root := parse(t, out)
			if root.attrs["xmlns"] != "http://www.w3.org/2000/svg" || root.attrs["role"] != "img" {
				t.Errorf("root attrs = %v", root.attrs)
			}
			if !root.hasClass(svgchart.ClassChart) || !root.hasClass("chart-"+strings.ToLower(r.name)) {
				t.Errorf("root class = %q", root.attrs["class"])
			}
			if _, bad := root.attrs["width"]; bad {
				t.Error("width attribute must be left to CSS")
			}
			if _, bad := root.attrs["height"]; bad {
				t.Error("height attribute must be left to CSS")
			}
			if _, bad := root.attrs["preserveAspectRatio"]; bad {
				t.Error("preserveAspectRatio must stay default")
			}
			if got := texts(root.byName("title")); !equal(got, []string{"各類別支出"}) {
				t.Errorf("title = %v", got)
			}
			if got, want := texts(root.byName("desc")), []string{"飲食: 1,200.00 TWD; 交通: 300.00 TWD; 其他"}; !equal(got, want) {
				t.Errorf("desc = %q, want %q", got, want)
			}
			// CJK passes through unescaped.
			if !strings.Contains(string(out), ">飲食<") {
				t.Errorf("CJK label was altered: %s", out)
			}
			// Title and desc are the first children, as the accessible name.
			if len(root.kids) < 2 || root.kids[0].name != "title" || root.kids[1].name != "desc" {
				t.Errorf("title and desc must come first")
			}
		})
	}
}

func TestProgress(t *testing.T) {
	tests := []struct {
		name         string
		spent, limit string
		wantClass    string // "" = no fill rect
		wantWidth    string
		wantMarker   string // "" = no marker
	}{
		{"within limit", "5", "10", svgchart.ClassFill, "100", "200"},
		{"exactly at limit is not over", "10", "10", svgchart.ClassFill, "200", "200"},
		{"over limit", "12", "10", svgchart.ClassFillOver, "200", "166.7"},
		{"just over", "10.01", "10", svgchart.ClassFillOver, "200", "199.8"},
		{"far over", "40", "10", svgchart.ClassFillOver, "200", "50"},
		{"nothing spent", "0", "10", "", "", "200"},
		{"negative spent is zero", "-5", "10", "", "", "200"},
		{"no limit nothing spent is an empty track", "0", "0", "", "", ""},
		{"no limit with spending is over", "5", "0", svgchart.ClassFillOver, "200", "0"},
		{"negative limit counts as no limit", "5", "-1", svgchart.ClassFillOver, "200", "0"},
		{"negative both", "-5", "-1", "", "", ""},
		{"small spent keeps the minimum", "1", "1000000000", svgchart.ClassFill, "2", "200"},
		{"fraction", "1", "3", svgchart.ClassFill, "66.7", "200"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title := "已花 " + tt.spent + " / 預算 " + tt.limit
			root := parse(t, svgchart.Progress(svgchart.Chart{ID: "p", Title: title}, d(tt.spent), d(tt.limit)))
			if !root.hasClass(svgchart.ClassProgress) || root.attrs["viewBox"] != "0 0 200 14" {
				t.Errorf("root = class %q viewBox %q", root.attrs["class"], root.attrs["viewBox"])
			}
			if tracks := root.byClass(svgchart.ClassTrack); len(tracks) != 1 || tracks[0].attrs["width"] != "200" {
				t.Errorf("want one full-width track, got %d", len(tracks))
			}
			fill, over := root.byClass(svgchart.ClassFill), root.byClass(svgchart.ClassFillOver)
			var got []*node
			got = append(got, fill...)
			got = append(got, over...)
			switch {
			case tt.wantClass == "":
				if len(got) != 0 {
					t.Errorf("fill drawn, want none: %v", attr(got, "class"))
				}
			case len(got) != 1 || !got[0].hasClass(tt.wantClass):
				t.Fatalf("fill classes = %v, want exactly one %s", attr(got, "class"), tt.wantClass)
			case got[0].attrs["width"] != tt.wantWidth:
				t.Errorf("fill width = %s, want %s", got[0].attrs["width"], tt.wantWidth)
			case got[0].attrs["x"] != "0":
				t.Errorf("fill starts at %s, want 0", got[0].attrs["x"])
			}
			lines := root.byClass(svgchart.ClassLimit)
			if tt.wantMarker == "" {
				if len(lines) != 0 {
					t.Errorf("limit marker drawn, want none")
				}
			} else if len(lines) != 1 || lines[0].name != "line" || lines[0].attrs["x1"] != tt.wantMarker || lines[0].attrs["x2"] != tt.wantMarker {
				t.Errorf("limit marker = %v, want a line at x=%s", attr(lines, "x1"), tt.wantMarker)
			}
			if got := texts(root.byName("desc")); !equal(got, []string{title}) {
				t.Errorf("desc = %q, want the title %q", got, title)
			}
			if got := texts(root.byName("title")); !equal(got, []string{title}) {
				t.Errorf("title = %q, want %q", got, title)
			}
		})
	}
}

func TestMalformedInputStaysWellFormed(t *testing.T) {
	bars := []svgchart.Bar{
		{Label: "a\x00b\x01c\xffd\u0085e\ufffe", Value: d("1"), Text: "\x1b[31mred\x7f", Highlight: true},
		{Label: strings.Repeat("長長", 5000), Value: d("2"), Text: strings.Repeat("9", 10000), Href: "/" + strings.Repeat("a", 10000)},
	}
	for _, r := range renderers {
		t.Run(r.name, func(t *testing.T) {
			root := parse(t, r.fn(svgchart.Chart{ID: "id", Title: "t\x00\xff"}, bars))
			labels := texts(root.byClass(svgchart.ClassLabel))
			// \x00, \x01, \xff and U+FFFE are dropped; U+0085 is a legal XML character and stays.
			if want := "abcd\u0085e"; labels[0] != want {
				t.Errorf("label = %q, want %q", labels[0], want)
			}
			if labels[1] != strings.Repeat("長長", 5000) {
				t.Errorf("very long label was altered (len %d)", len(labels[1]))
			}
		})
	}
	parse(t, svgchart.Progress(svgchart.Chart{ID: "p", Title: strings.Repeat("長", 5000) + "\x00"}, d("1"), d("2")))
}

var numeric = regexp.MustCompile(`^\d+(\.\d)?$`)

// allowedAttrs is every attribute the package may emit: no style, no event
// handler, no external reference.
var allowedAttrs = map[string]bool{
	"xmlns": true, "viewBox": true, "class": true, "role": true, "aria-labelledby": true, "id": true,
	"x": true, "y": true, "width": true, "height": true, "x1": true, "y1": true, "x2": true, "y2": true,
	"text-anchor": true, "href": true,
}

var allowedElements = map[string]bool{
	"svg": true, "title": true, "desc": true, "rect": true, "line": true, "text": true, "a": true,
}

// checkSafe asserts the output of one chart is restricted to inert, local
// content and that all geometry is plain fixed-point numbers.
func checkSafe(t *testing.T, root *node) {
	t.Helper()
	root.walk(func(n *node) {
		if !allowedElements[n.name] {
			t.Errorf("element <%s> is not allowed", n.name)
		}
		for name, v := range n.attrs {
			if !allowedAttrs[name] {
				t.Errorf("attribute %s on <%s> is not allowed", name, n.name)
			}
			switch name {
			case "x", "y", "width", "height", "x1", "y1", "x2", "y2":
				if !numeric.MatchString(v) {
					t.Errorf("<%s %s=%q> is not a plain number", n.name, name, v)
				}
			case "href":
				if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
					t.Errorf("href %q is not site-relative", v)
				}
			}
		}
	})
	vb := strings.Fields(root.attrs["viewBox"])
	if len(vb) != 4 {
		t.Errorf("viewBox = %q", root.attrs["viewBox"])
	}
	for _, v := range vb {
		if !numeric.MatchString(v) {
			t.Errorf("viewBox part %q is not a plain number", v)
		}
	}
}

func TestOutputIsInertWellFormedAndPlain(t *testing.T) {
	progress := [][2]string{{"0", "0"}, {"1", "0"}, {"0", "1"}, {"5", "10"}, {"10", "10"}, {"11", "10"}, {"-1", "-1"},
		{"1000000000000000000000000000000", "1"}, {"0.000000001", "1000000000000000000000000000000"}, {"1", "3"}}
	for name, bars := range corpus() {
		for _, r := range renderers {
			t.Run(r.name+"/"+name, func(t *testing.T) {
				out := r.fn(chart, bars)
				root := parse(t, out)
				checkSafe(t, root)
				if s := string(out); strings.Contains(s, "<script") || strings.Contains(s, "<style") || strings.Contains(s, " style=") {
					t.Errorf("forbidden markup in output")
				}
				if again := r.fn(chart, bars); again != out {
					t.Errorf("output is not deterministic")
				}
			})
		}
	}
	for _, p := range progress {
		t.Run("Progress/"+p[0]+"/"+p[1], func(t *testing.T) {
			out := svgchart.Progress(chart, d(p[0]), d(p[1]))
			checkSafe(t, parse(t, out))
			if again := svgchart.Progress(chart, d(p[0]), d(p[1])); again != out {
				t.Errorf("output is not deterministic")
			}
		})
	}
}

func TestDeterministicAcrossEqualValues(t *testing.T) {
	// The same number written two ways (scale differs) must draw the same chart.
	a := svgchart.HBars(chart, []svgchart.Bar{{Label: "x", Value: d("1.50"), Text: "t"}, {Label: "y", Value: d("3"), Text: "t"}})
	b := svgchart.HBars(chart, []svgchart.Bar{{Label: "x", Value: d("1.5"), Text: "t"}, {Label: "y", Value: d("3.000"), Text: "t"}})
	if a != b {
		t.Errorf("equal values with different scales drew differently:\n%s\n%s", a, b)
	}
}

func TestCJKPassesThroughUnescaped(t *testing.T) {
	out := string(svgchart.HBars(svgchart.Chart{ID: "c", Title: "本月飲食"}, []svgchart.Bar{{Label: "飲食", Value: d("1"), Text: "1 元"}}))
	for _, want := range []string{">飲食<", ">1 元<", ">本月飲食<"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "&#") {
		t.Errorf("CJK was numerically escaped: %s", out)
	}
}

// valueTextWidth is the test's own estimate of a value text at the 12px font of
// the stylesheet: letters and digits 8 units, ',' '.' ' ' 4, CJK 12.
func valueTextWidth(s string) decimal.Decimal {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1100:
			w += 12
		case r == ',' || r == '.' || r == ' ':
			w += 4
		default:
			w += 8
		}
	}
	return decimal.NewFromInt(int64(w))
}

// valueBox returns the horizontal extent a value text covers, from its anchor.
func valueBox(t *testing.T, v *node) (from, to decimal.Decimal) {
	t.Helper()
	x, w := d(v.attrs["x"]), valueTextWidth(v.text)
	switch v.attrs["text-anchor"] {
	case "start":
		return x, x.Add(w)
	case "end":
		return x.Sub(w), x
	case "middle":
		return x.Sub(w.Div(d("2"))), x.Add(w.Div(d("2")))
	}
	t.Fatalf("unexpected anchor %q", v.attrs["text-anchor"])
	return from, to
}

// checkValueTexts asserts, for every value text of the rendered columns, that
// there is one per highlighted bar with a Text, that its box stays inside the
// viewBox, that no bar taller than its baseline lies under the box, and that
// the baseline sits 4 above the tallest bar under the box (or above the chart
// baseline when the box covers no bar).
func checkValueTexts(t *testing.T, bars []svgchart.Bar) []*node {
	t.Helper()
	root := parse(t, svgchart.Columns(chart, bars))
	var want []string
	for _, b := range bars {
		if b.Highlight && b.Text != "" {
			want = append(want, b.Text)
		}
	}
	values := root.byClass(svgchart.ClassValue)
	if got := texts(values); !equal(got, want) {
		t.Fatalf("value texts = %v, want %v", got, want)
	}
	rects := root.byClass(svgchart.ClassBar)
	for _, v := range values {
		from, to := valueBox(t, v)
		y := d(v.attrs["y"])
		if from.Sign() < 0 || to.GreaterThan(d("360")) {
			t.Errorf("text %q spans %s..%s, outside the viewBox", v.text, from, to)
		}
		if y.LessThan(d("12")) || y.GreaterThan(d("170")) {
			t.Errorf("text %q baseline y=%s leaves the viewBox (12px high)", v.text, y)
		}
		wantY := d("140")
		for _, r := range rects {
			x, w, top := d(r.attrs["x"]), d(r.attrs["width"]), d(r.attrs["y"])
			if !x.LessThan(to) || !x.Add(w).GreaterThan(from) {
				continue
			}
			if top.LessThan(y) {
				t.Errorf("text %q at y=%s x=%s..%s overlaps the bar at x=%s..%s whose top is y=%s", v.text, y, from, to, x, x.Add(w), top)
			}
			wantY = decimal.Min(wantY, top)
		}
		if !y.Equal(wantY.Sub(d("4"))) {
			t.Errorf("text %q baseline y=%s, want 4 above the tallest bar under it (%s)", v.text, y, wantY.Sub(d("4")))
		}
	}
	return values
}

// withValues builds bars whose Text is a typical amount, highlighting the
// given indexes.
func withValues(values []string, highlighted ...int) []svgchart.Bar {
	bars := vals(values...)
	for i := range bars {
		bars[i].Text = "16,865.00 TWD"
	}
	for _, i := range highlighted {
		bars[i].Highlight = true
	}
	return bars
}

func TestColumnsValueTextClearsTallerNeighbours(t *testing.T) {
	tests := []struct {
		name        string
		values      []string
		highlighted []int
	}{
		{"shorter than both neighbours", []string{"100", "100", "100", "20", "100", "100", "100"}, []int{3}},
		{"shorter than its left neighbours", []string{"100", "100", "100", "100", "20", "5", "5"}, []int{4}},
		{"shorter than its right neighbours", []string{"5", "5", "20", "100", "100", "100", "100"}, []int{2}},
		{"first column shorter", []string{"20", "100", "100", "100", "100", "100", "100"}, []int{0}},
		{"last column shorter", []string{"100", "100", "100", "100", "100", "100", "20"}, []int{6}},
		{"first of twelve", []string{"1", "9", "9", "9", "9", "9", "9", "9", "9", "9", "9", "9"}, []int{0}},
		{"last of twelve", []string{"9", "9", "9", "9", "9", "9", "9", "9", "9", "9", "9", "1"}, []int{11}},
		{"single bar", []string{"5"}, []int{0}},
		{"taller than its neighbours", []string{"20", "20", "100", "20", "20"}, []int{2}},
		{"two highlighted", []string{"100", "10", "10", "100", "100", "10", "100"}, []int{1, 5}},
		{"all highlighted", []string{"100", "10", "50"}, []int{0, 1, 2}},
		{"zero highlighted between tall", []string{"100", "0", "100"}, []int{1}},
		{"zero highlighted first", []string{"0", "100", "100"}, []int{0}},
		{"all zero", []string{"0", "0", "0"}, []int{1}},
		{"zero and negative", []string{"-3", "0", "7"}, []int{0, 1}},
		{"ascending to the highlighted last", []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}, []int{11}},
		{"descending to the highlighted last", []string{"12", "11", "10", "9", "8", "7", "6", "5", "4", "3", "2", "1"}, []int{11}},
		{"thirty-one days", []string{"9", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1"}, []int{15, 30}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValueTexts(t, withValues(tt.values, tt.highlighted...))
		})
	}

	t.Run("highlighted without Text draws none", func(t *testing.T) {
		bars := withValues([]string{"100", "20", "100"}, 1)
		bars[1].Text = ""
		checkValueTexts(t, bars)
	})
	t.Run("CJK text", func(t *testing.T) {
		bars := withValues([]string{"100", "100", "20", "100", "100"}, 2)
		bars[2].Text = "本月合計一千六百元"
		checkValueTexts(t, bars)
	})
}

// The 12-month trend of /app/spending: the current month is last and the
// shortest; its text runs left over the previous months, so it sits on the
// top of the tallest of the four columns it spans (the 9th, value 4).
func TestColumnsValueTextOverShortLastColumn(t *testing.T) {
	values := withValues([]string{"12", "11", "10", "9", "8", "7", "6", "5", "4", "3", "2", "1"}, 11)
	v := checkValueTexts(t, values)[0]
	if v.attrs["text-anchor"] != "end" || v.attrs["x"] != "350.1" || v.attrs["y"] != "101.3" {
		t.Errorf("text anchor=%s x=%s y=%s, want end 350.1 101.3", v.attrs["text-anchor"], v.attrs["x"], v.attrs["y"])
	}
}

// A highlighted column taller than everything its text spans keeps the text
// directly above itself.
func TestColumnsValueTextOverTallestColumn(t *testing.T) {
	v := checkValueTexts(t, withValues([]string{"20", "20", "100", "20", "20"}, 2))[0]
	if v.attrs["text-anchor"] != "middle" || v.attrs["y"] != "32" {
		t.Errorf("text anchor=%s y=%s, want middle 32", v.attrs["text-anchor"], v.attrs["y"])
	}
}
