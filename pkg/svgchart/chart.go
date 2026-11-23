package svgchart

import (
	"html"
	"html/template"
	"slices"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

// Class names are the contract with the external stylesheet: the package emits
// them and never any inline style.
const (
	// ClassChart is on the root <svg> of every chart.
	ClassChart = "chart"
	// ClassHBars is added to the root <svg> of an HBars chart.
	ClassHBars = "chart-hbars"
	// ClassColumns is added to the root <svg> of a Columns chart.
	ClassColumns = "chart-columns"
	// ClassProgress is added to the root <svg> of a Progress chart.
	ClassProgress = "chart-progress"
	// ClassLabel is on the <text> naming a bar or a column.
	ClassLabel = "chart-label"
	// ClassValue is on the <text> showing a bar's value as the caller formatted it.
	ClassValue = "chart-value"
	// ClassTrack is on the background <rect> a bar or the meter is drawn on.
	ClassTrack = "chart-track"
	// ClassBar is on the <rect> of one bar or column.
	ClassBar = "chart-bar"
	// ClassBarHighlight is added next to ClassBar on the highlighted bar.
	ClassBarHighlight = "chart-bar-hl"
	// ClassFill is on the <rect> of a Progress meter that is within its limit.
	ClassFill = "chart-fill"
	// ClassFillOver is on the <rect> of a Progress meter that exceeds its limit.
	ClassFillOver = "chart-fill-over"
	// ClassLimit is on the <line> that marks the limit of a Progress meter.
	ClassLimit = "chart-limit"
	// ClassNarrow is added to the root <svg> of a chart whose canvas is narrower than the
	// default: it is drawn for a phone, and the stylesheet shows it there in place of the
	// chart of the default width.
	ClassNarrow = "chart-narrow"
	// ClassAxis is on the <line> that marks zero under the columns.
	ClassAxis = "chart-axis"
	// ClassGrid is on the <line> across the height the largest column reaches.
	ClassGrid = "chart-grid"
	// ClassScale is on the <text> that says what the largest column is.
	ClassScale = "chart-scale"
	// ClassSub is on the <text> under the label of a column, such as its year.
	ClassSub = "chart-sub"
)

// DefaultWidth is the width of the canvas of a chart, in viewBox units, when its Width is
// not set.
const DefaultWidth = 360

// Geometry, in viewBox units.
const (
	// minLength is the shortest a nonzero bar is drawn, so it stays visible
	// next to a huge one.
	minLength = 2

	hbarsRowHeight = 36
	hbarsPad       = 4
	hbarsTrack     = 240 // the bar track at the default width: about 2/3 of it
	hbarsBarHeight = 10
	hbarsLabelY    = 12 // label baseline, from the top of the row
	hbarsBarY      = 18 // bar top, from the top of the row
	hbarsValueY    = 27 // value baseline, from the top of the row
	hbarsValueGap  = 8  // between the end of the track and the widest value, on any other width
	hbarsMinTrack  = 48

	columnsHeight    = 170
	columnsPad       = 4
	columnsBaseline  = 140 // bottom of every column
	columnsMaxHeight = 104 // height of the tallest column
	columnsLabelY    = 160
	columnsSubY      = 173 // baseline of the second line under a label
	columnsSubExtra  = 14  // what that line adds to the height
	columnsMaxWidth  = 48
	columnsValueGap  = 4 // between a highlighted column's text baseline and the tallest bar under the text

	// The width of a value text is estimated, not measured: the package knows
	// neither the font nor the renderer. The units are generous for the 12px
	// sans-serif of the stylesheet, so the estimate errs on the wide side.
	valueRuneWidth     = 8  // letters, digits, symbols
	valueNarrowRuneW   = 4  // ',', '.', ' '
	valueWideRuneWidth = 12 // CJK and everything else at or above valueWideFrom
	valueWideFrom      = 0x1100

	progressWidth  = 200
	progressHeight = 14
	progressTrackY = 2
	progressBarH   = 10
)

// divScale is the number of decimal places kept by the divisions of the layout.
const divScale = 8

// Chart names a chart for assistive technology, and says how wide it is drawn.
type Chart struct {
	// ID is unique within the page; it prefixes the ids of <title>/<desc>.
	// Only ASCII letters, digits, '-' and '_' are kept; every other rune becomes
	// '-'; empty becomes "chart".
	ID string
	// Title is plain text (escaped by the package), shown as <title>.
	Title string
	// Width is the width of the canvas in viewBox units, DefaultWidth when zero. A canvas
	// is shown as wide as the room it has, so its 12 units of text are as big on a phone
	// as the canvas is narrow: on a 320 pixel screen the text of a chart drawn 240 wide is
	// a third bigger than that of one drawn 360 wide. A canvas narrower than the default is
	// marked ClassNarrow, and the caller draws the chart of the default width beside it,
	// for the screens that have the room.
	Width int
	// MarkMax (Columns) writes the Text of the largest column over a dashed line across the
	// height it reaches, as the figure the other columns are read against.
	MarkMax bool
	// NumbersApart (Columns) leaves out the value that a highlighted column shows over
	// itself: the caller writes the numbers next to the chart, where they cannot be taken
	// for those of another column.
	NumbersApart bool
}

// width is the width of the canvas.
func (c Chart) width() int {
	if c.Width > 0 {
		return c.Width
	}
	return DefaultWidth
}

// Bar is one bar of HBars or one column of Columns.
type Bar struct {
	// Label is plain text, escaped.
	Label string
	// Value is the size of the bar; a negative Value is drawn as zero.
	Value decimal.Decimal
	// Text is the value as the caller formats it (e.g. "1,234.00 TWD"); plain
	// text, escaped; may be empty.
	Text string
	// Href is an optional link. Only a site-relative path starting with a single
	// "/" is honoured; anything else is dropped and the bar is drawn without a
	// link.
	Href string
	// Highlight marks the selected bar.
	Highlight bool
	// Sub (Columns) is a second, smaller line under the Label, such as the year the month
	// is of; plain text, escaped; empty for none.
	Sub string
}

// HBars draws one horizontal bar per entry, in the caller's order: the label on
// its own line and, under it, the bar with its Text right-aligned at the far
// right of the same line. A label wider than the canvas is fitted to it. It
// returns the empty string for no bars.
func HBars(c Chart, bars []Bar) template.HTML {
	if len(bars) == 0 {
		return ""
	}
	width := c.width()
	track := hbarsTrackOf(width, bars)
	maxValue := maxOf(bars)
	full := decimal.NewFromInt(int64(track))

	var s svg
	s.begin(c, kindOf(ClassHBars, width), width, len(bars)*hbarsRowHeight+2*hbarsPad, describe(bars))
	for i, b := range bars {
		y0 := hbarsPad + i*hbarsRowHeight
		s.openLink(b.Href)
		s.fitText(ClassLabel, "0", itoa(y0+hbarsLabelY), "start", b.Label, width)
		s.rect(ClassTrack, "0", itoa(y0+hbarsBarY), itoa(track), itoa(hbarsBarHeight))
		if w := length(b.Value, maxValue, full); w.Sign() > 0 {
			s.rect(barClass(b), "0", itoa(y0+hbarsBarY), num(w), itoa(hbarsBarHeight))
		}
		if b.Text != "" {
			s.text(ClassValue, itoa(width), itoa(y0+hbarsValueY), "end", b.Text)
		}
		s.closeLink(b.Href)
	}
	return s.finish()
}

// hbarsTrackOf is the length of the track a bar is drawn on. At the default width it is
// the same whatever is written beside it, as it always was; on any other canvas it is
// what the widest value leaves of the width, so that a value never runs over its bar.
func hbarsTrackOf(width int, bars []Bar) int {
	if width == DefaultWidth {
		return hbarsTrack
	}
	widest := 0
	for _, b := range bars {
		widest = max(widest, valueWidth(b.Text))
	}
	return max(width-widest-hbarsValueGap, hbarsMinTrack)
}

// Columns draws the bars as columns side by side, in the caller's order, with a
// label under every column (and under it the Sub of the column, when it has
// one) and a line along zero. Only the highlighted columns show their Text above
// them, on the baseline of the tallest column the text spans so that it never
// runs over a taller neighbour; the other numbers belong in the table next to
// the chart. The text is centred on its column, or anchored to the nearer side
// of the chart when the centred text would leave it. NumbersApart leaves that
// text out, and MarkMax writes the Text of the largest column over a line at its
// height. It returns the empty string for no bars.
func Columns(c Chart, bars []Bar) template.HTML {
	if len(bars) == 0 {
		return ""
	}
	width := c.width()
	height := columnsHeight
	if slices.ContainsFunc(bars, func(b Bar) bool { return b.Sub != "" }) {
		height += columnsSubExtra
	}
	maxValue := maxOf(bars)
	full := decimal.NewFromInt(columnsMaxHeight)
	slot := decimal.NewFromInt(int64(width-2*columnsPad)).DivRound(decimal.NewFromInt(int64(len(bars))), divScale)
	barWidth := decimal.Min(
		slot.Mul(decimal.NewFromInt(3)).DivRound(decimal.NewFromInt(5), divScale),
		decimal.NewFromInt(columnsMaxWidth),
	)
	half := decimal.NewFromInt(2)
	baseline := decimal.NewFromInt(columnsBaseline)

	// Every column's centre, left edge and top (the baseline for an empty one).
	centres := make([]decimal.Decimal, len(bars))
	lefts := make([]decimal.Decimal, len(bars))
	tops := make([]decimal.Decimal, len(bars))
	for i, b := range bars {
		left := decimal.NewFromInt(columnsPad).Add(slot.Mul(decimal.NewFromInt(int64(i))))
		centres[i] = left.Add(slot.DivRound(half, divScale))
		lefts[i] = centres[i].Sub(barWidth.DivRound(half, divScale))
		tops[i] = baseline.Sub(length(b.Value, maxValue, full))
	}

	var s svg
	s.begin(c, kindOf(ClassColumns, width), width, height, describe(bars))
	if c.MarkMax && maxValue.Sign() > 0 {
		s.markMax(bars, maxValue, width)
	}
	for i, b := range bars {
		s.openLink(b.Href)
		if h := baseline.Sub(tops[i]); h.Sign() > 0 {
			s.rect(barClass(b), num(lefts[i]), num(tops[i]), num(barWidth), num(h))
		}
		s.text(ClassLabel, num(centres[i]), itoa(columnsLabelY), "middle", b.Label)
		if b.Sub != "" {
			s.text(ClassSub, num(centres[i]), itoa(columnsSubY), "middle", b.Sub)
		}
		if b.Highlight && b.Text != "" && !c.NumbersApart {
			anchor, ax, from, to := valueSpan(b.Text, centres[i], lefts[i], barWidth, decimal.NewFromInt(int64(width)))
			top := tops[i]
			for j := range bars {
				if lefts[j].LessThan(to) && lefts[j].Add(barWidth).GreaterThan(from) {
					top = decimal.Min(top, tops[j])
				}
			}
			s.text(ClassValue, num(ax), num(top.Sub(decimal.NewFromInt(columnsValueGap))), anchor, b.Text)
		}
		s.closeLink(b.Href)
	}
	s.line(ClassAxis, itoa(columnsPad), itoa(columnsBaseline), itoa(width-columnsPad), itoa(columnsBaseline))
	return s.finish()
}

// valueSpan places the text of the column with the given centre and left edge on a canvas of
// the given width: centred when that stays inside the chart, else anchored to the column's
// left edge (text running right) or right edge (text running left). It returns the
// anchor, the x of the anchor and the horizontal extent [from, to] the text is
// estimated to cover.
func valueSpan(text string, centre, left, barWidth, width decimal.Decimal) (anchor string, x, from, to decimal.Decimal) {
	w := decimal.NewFromInt(int64(valueWidth(text)))
	half := w.DivRound(decimal.NewFromInt(2), divScale)
	switch {
	case centre.Sub(half).Sign() < 0:
		return "start", left, left, left.Add(w)
	case centre.Add(half).GreaterThan(width):
		right := left.Add(barWidth)
		return "end", right, right.Sub(w), right
	default:
		return "middle", centre, centre.Sub(half), centre.Add(half)
	}
}

// valueWidth estimates the width of a value text in viewBox units, from the
// 12px font size the stylesheet gives to every chart text.
func valueWidth(text string) int {
	w := 0
	for _, r := range text {
		switch {
		case r >= valueWideFrom:
			w += valueWideRuneWidth
		case r == ',' || r == '.' || r == ' ':
			w += valueNarrowRuneW
		default:
			w += valueRuneWidth
		}
	}
	return w
}

// Progress draws one meter: spent against limit on a scale of the larger of the
// two. The fill runs from 0 to spent and is drawn as over the limit (and only
// then) when spent exceeds limit; a line marks the limit. A negative spent or
// limit counts as zero. The description is c.Title alone, so the caller passes
// the whole sentence there. Zero spent against zero limit draws an empty track.
func Progress(c Chart, spent, limit decimal.Decimal) template.HTML {
	spent = decimal.Max(spent, decimal.Zero)
	limit = decimal.Max(limit, decimal.Zero)
	scale := decimal.Max(spent, limit)
	full := decimal.NewFromInt(progressWidth)

	var s svg
	s.begin(c, ClassProgress, progressWidth, progressHeight, c.Title)
	s.rect(ClassTrack, "0", itoa(progressTrackY), itoa(progressWidth), itoa(progressBarH))
	if scale.Sign() > 0 {
		if fill := length(spent, scale, full); fill.Sign() > 0 {
			class := ClassFill
			if spent.GreaterThan(limit) {
				class = ClassFillOver
			}
			s.rect(class, "0", itoa(progressTrackY), num(fill), itoa(progressBarH))
		}
		x := num(limit.DivRound(scale, divScale).Mul(full))
		s.line(ClassLimit, x, "0", x, itoa(progressHeight))
	}
	return s.finish()
}

// svg accumulates one <svg> element.
type svg struct {
	b strings.Builder
}

// write appends the parts verbatim; callers pass only escaped or fixed text.
func (s *svg) write(parts ...string) {
	for _, p := range parts {
		s.b.WriteString(p)
	}
}

// begin writes the opening tag, <title> and <desc>.
func (s *svg) begin(c Chart, kind string, width, height int, desc string) {
	id := sanitizeID(c.ID)
	s.write(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `, itoa(width), " ", itoa(height),
		`" class="`, ClassChart, " ", kind, `" role="img" aria-labelledby="`, id, "-title ", id, `-desc">`,
		`<title id="`, id, `-title">`, esc(c.Title), `</title>`,
		`<desc id="`, id, `-desc">`, esc(desc), `</desc>`,
	)
}

// finish closes the element and returns it.
func (s *svg) finish() template.HTML {
	s.write("</svg>")
	return template.HTML(s.b.String()) // every caller string was escaped by esc
}

// rect writes a <rect>.
func (s *svg) rect(class, x, y, w, h string) {
	s.write(`<rect class="`, class, `" x="`, x, `" y="`, y, `" width="`, w, `" height="`, h, `"/>`)
}

// line writes a <line>.
func (s *svg) line(class, x1, y1, x2, y2 string) {
	s.write(`<line class="`, class, `" x1="`, x1, `" y1="`, y1, `" x2="`, x2, `" y2="`, y2, `"/>`)
}

// text writes a <text> whose content is escaped here.
func (s *svg) text(class, x, y, anchor, content string) {
	s.write(`<text class="`, class, `" x="`, x, `" y="`, y, `" text-anchor="`, anchor, `">`, esc(content), `</text>`)
}

// fitText writes a <text> like text does, and fits it to limit units when its content is
// estimated to be wider than that: the glyphs are squeezed together so that the end of a
// long name is not cut off by the edge of the canvas.
func (s *svg) fitText(class, x, y, anchor, content string, limit int) {
	if valueWidth(content) <= limit {
		s.text(class, x, y, anchor, content)
		return
	}
	s.write(`<text class="`, class, `" x="`, x, `" y="`, y, `" text-anchor="`, anchor,
		`" textLength="`, itoa(limit), `" lengthAdjust="spacingAndGlyphs">`, esc(content), `</text>`)
}

// markMax writes what the largest column is over a dashed line across the height it
// reaches: the Text of the first of the columns that is as large as the largest.
func (s *svg) markMax(bars []Bar, maxValue decimal.Decimal, width int) {
	top := columnsBaseline - columnsMaxHeight
	s.line(ClassGrid, itoa(columnsPad), itoa(top), itoa(width-columnsPad), itoa(top))
	for _, b := range bars {
		if b.Value.Equal(maxValue) {
			if b.Text != "" {
				s.text(ClassScale, itoa(columnsPad), itoa(top-columnsValueGap), "start", b.Text)
			}
			return
		}
	}
}

// kindOf is the class of the root <svg> of a chart of the given kind, which says that its
// canvas is narrower than the default when it is.
func kindOf(kind string, width int) string {
	if width < DefaultWidth {
		return kind + " " + ClassNarrow
	}
	return kind
}

// openLink starts an <a> when href is a safe site-relative path.
func (s *svg) openLink(href string) {
	if safeHref(href) {
		s.write(`<a href="`, esc(href), `">`)
	}
}

// closeLink ends the <a> started by openLink for the same href.
func (s *svg) closeLink(href string) {
	if safeHref(href) {
		s.write("</a>")
	}
}

// safeHref reports whether href is a site-relative path: it starts with exactly
// one "/" and holds no backslash, whitespace or control character (browsers
// drop tabs and newlines and read "\" as "/", which would turn "/\x" or
// "/<tab>/x" into a protocol-relative URL).
func safeHref(href string) bool {
	if !strings.HasPrefix(href, "/") || strings.HasPrefix(href, "//") {
		return false
	}
	for _, r := range href {
		if r == '\\' || r <= ' ' || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return false
		}
	}
	return true
}

// barClass returns the class of a bar's <rect>.
func barClass(b Bar) string {
	if b.Highlight {
		return ClassBar + " " + ClassBarHighlight
	}
	return ClassBar
}

// describe lists every bar as "label: text" joined by "; ", so assistive
// technology gets the same numbers as the table beside the chart. The Sub of a
// bar is part of its label.
func describe(bars []Bar) string {
	parts := make([]string, len(bars))
	for i, b := range bars {
		label := b.Label
		if b.Sub != "" {
			label += " " + b.Sub
		}
		if b.Text == "" {
			parts[i] = label
			continue
		}
		parts[i] = label + ": " + b.Text
	}
	return strings.Join(parts, "; ")
}

// maxOf returns the largest value among the bars, at least zero.
func maxOf(bars []Bar) decimal.Decimal {
	m := decimal.Zero
	for _, b := range bars {
		m = decimal.Max(m, b.Value)
	}
	return m
}

// length returns value/maxValue*full, 0 when value or maxValue is not
// positive, and never less than minLength for a positive value.
func length(value, maxValue, full decimal.Decimal) decimal.Decimal {
	if value.Sign() <= 0 || maxValue.Sign() <= 0 {
		return decimal.Zero
	}
	l := value.DivRound(maxValue, divScale).Mul(full)
	return decimal.Min(decimal.Max(l, decimal.NewFromInt(minLength)), full)
}

// num prints d rounded to one decimal place, without a trailing ".0".
func num(d decimal.Decimal) string {
	return strings.TrimSuffix(d.Round(1).StringFixed(1), ".0")
}

// itoa prints an int.
func itoa(n int) string {
	return strconv.Itoa(n)
}

// sanitizeID keeps ASCII letters, digits, '-' and '_', turns every other rune
// into '-', and falls back to "chart" for an empty id.
func sanitizeID(id string) string {
	if id == "" {
		return "chart"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// esc makes caller text safe for an XML text node or attribute: invalid UTF-8
// and characters XML 1.0 cannot carry are dropped, then the markup characters
// are escaped.
func esc(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t', r == '\n', r == '\r',
			r >= 0x20 && r <= 0xD7FF,
			r >= 0xE000 && r <= 0xFFFD,
			r >= 0x10000 && r <= 0x10FFFF:
			return r
		}
		return -1
	}, s)
	return html.EscapeString(s)
}
