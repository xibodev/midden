// Package quotation compares visible source words, not Markdown styling.
package quotation

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
)

func visibleText(text string) string {
	var out strings.Builder
	ast.WalkFunc(markdown.Parse([]byte(text), nil), func(node ast.Node, entering bool) ast.WalkStatus {
		if leaf := node.AsLeaf(); entering && leaf != nil {
			out.Write(leaf.Literal)
		}
		if !entering {
			switch node.(type) {
			case *ast.Paragraph, *ast.Heading, *ast.ListItem, *ast.TableCell, *ast.TableRow:
				out.WriteByte(' ')
			}
		}
		if _, ok := node.(*ast.Hardbreak); ok && entering {
			out.WriteByte(' ')
		}
		return ast.GoToNext
	})
	return strings.Join(strings.Fields(out.String()), " ")
}

func word(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func Matches(source, quoted string) bool {
	source, quoted = visibleText(source), visibleText(quoted)
	if quoted == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(quoted)
	last, _ := utf8.DecodeLastRuneInString(quoted)
	for offset := 0; offset <= len(source)-len(quoted); {
		found := strings.Index(source[offset:], quoted)
		if found < 0 {
			return false
		}
		start := offset + found
		end := start + len(quoted)
		left, right := true, true
		if start > 0 && word(first) {
			r, _ := utf8.DecodeLastRuneInString(source[:start])
			left = !word(r)
		}
		if end < len(source) && word(last) {
			r, _ := utf8.DecodeRuneInString(source[end:])
			right = !word(r)
		}
		if left && right {
			return true
		}
		_, width := utf8.DecodeRuneInString(source[start:])
		offset = start + width
	}
	return false
}
