// Package svgchart turns plain data into small inline SVG charts for
// server-rendered pages: horizontal bars, columns and a single progress meter.
//
// The package is pure and business-agnostic. Every function returns one <svg>
// element as html/template.HTML, so a template can embed it without further
// escaping, and the same input always produces byte-identical output.
//
// All geometry is computed with shopspring decimals (never float64) and printed
// with at most one decimal place, so the output never contains exponents, NaN
// or a negative zero.
//
// The SVG carries no style attribute, style element, script, event handler or
// external reference, so it is safe under a "default-src 'self'" Content
// Security Policy. Appearance is left to an external stylesheet that targets
// the Class* constants. Caller text (titles, labels, values, links) is always
// escaped, both in text nodes and in attributes.
package svgchart
