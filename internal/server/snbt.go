package server

import (
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// Spaceless SNBT for /item modify's modifier argument.
//
// A modifier is one chat token, so the reader only ever sees SNBT
// without whitespace: compounds, lists, byte/int/long arrays, quoted
// and bare strings, and numbers. The parser below reads that subset
// and prints it back canonically - sorted keys, quoted strings,
// suffixed numbers - because the real server echoes what it read that
// way in its failures ("No key function in MapLike[{foo:\"bar\"}]" for
// an input written {foo:bar}).
//
// Error positions are offsets within the token, which errTokenOff turns
// into fail marks. Anything the probes did not pin is marked as a
// guess where it is made.

// snbtKind is one parsed value's shape.
type snbtKind int

const (
	snbtCompound snbtKind = iota
	snbtList
	snbtString
	snbtNumber
)

// snbtTag is one parsed value: a compound's entries, a list's items, a
// string, or a number kept in its canonical text (1.5d, 1b, -2). end is
// the offset just past the value, which is where the real server
// reports an id it read there but does not hold. quoted tells a quoted
// string from a bare word: after a complete value the reader wants ","
// for the former and "(" for the latter. numWant is what the reader
// reaches for after a complete number: "." for an integer, "e|E" for a
// dotted double.
type snbtTag struct {
	kind    snbtKind
	str     string
	num     string
	vals    map[string]*snbtTag
	items   []*snbtTag
	end     int
	quoted  bool
	numWant string
}

// snbtError is a grammar failure at an offset within the token.
type snbtError struct {
	key  string
	with []any
	pos  int
}

// snbtLit builds a literal.incorrect failure expecting arg at pos.
func snbtLit(pos int, arg string) *snbtError {
	return &snbtError{key: "argument.literal.incorrect", with: []any{arg}, pos: pos}
}

// isBareChar reports whether c continues an unquoted word.
func isBareChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '_' || c == '.' ||
		c == '+' || c == '-'
}

// parseSNBTTag reads one value at i. A character that cannot start a
// value fails wanting an unquoted string at the end: {a:} and {foo:}
// both point past the token rather than at the brace.
func parseSNBTTag(s string, i int) (*snbtTag, int, *snbtError) {
	if i < len(s) {
		switch s[i] {
		case '{':
			return parseSNBTCompound(s, i)
		case '[':
			return parseSNBTList(s, i)
		case '"', '\'':
			str, next, ok := parseSNBTQuoted(s, i)
			if !ok {
				return nil, i, &snbtError{key: "snbt.parser.invalid_string_contents", pos: len(s)}
			}
			return &snbtTag{kind: snbtString, str: str, end: next, quoted: true}, next, nil
		}
		if isBareChar(s[i]) {
			return parseSNBTWord(s, i)
		}
	}
	// A character that cannot start a value fails there, not at the
	// end: [1,,2] points at the second comma.
	return nil, i, &snbtError{key: "snbt.parser.expected_unquoted_string", pos: i}
}

// parseSNBTWord classifies the word at i: a hex number takes the hex
// path, a number token the number path, anything else the maximal bare
// word (true and false are bytes, the rest strings).
func parseSNBTWord(s string, i int) (*snbtTag, int, *snbtError) {
	if xj, isHex := snbtPlainHexHead(s, i); isHex {
		if tag, next, herr, done := parseSNBTHex(s, i, xj); done {
			return tag, next, herr
		}
		return snbtWordFallback(s, i)
	}
	body, suffix, end, ok, xpos := snbtNumberToken(s, i)
	if ok {
		if berr := snbtBadExp(body, end); berr != nil {
			// The exponent marker commits: [1e] ends wanting "+".
			return nil, end, berr
		}
		if snbtBadZero(body) && suffix != 'f' && suffix != 'F' && suffix != 'd' && suffix != 'D' {
			// A leading zero ends the number where the token does:
			// [007] ends wanting "." at the closer. A float suffix
			// still reads a value ([007f] is [7.0f], [007d] is
			// [7.0d]), but any other suffix - and any letter or sign
			// after the digits - reads as a word: [01b], [01L],
			// [007s] and [007+5] all end wanting "(" past
			// themselves. Underscores after a zero are probe-pending.
			if suffix != 0 || (end < len(s) && (isWordLetter(s[end]) || s[end] == '+' || s[end] == '-')) {
				return snbtWordFallback(s, i)
			}
			return nil, end, snbtLit(end, ".")
		}
		// After digits-dot the reader still wants fraction digits, an
		// exponent, or the value's end: a second dot is not an
		// exponent marker, and neither is anything else that cannot
		// continue the number - [1..5], [1.+5] and [-5.x] all end
		// wanting a decimal numeral where the dot's tail stopped,
		// and a dot with nothing after it at all does too ([1.
		// ends wanting one at the end).
		if suffix == 0 && strings.HasSuffix(body, ".") {
			if end >= len(s) {
				return nil, end, &snbtError{key: "snbt.parser.expected_decimal_numeral", pos: end}
			}
			if c := s[end]; c != ',' && c != ']' && c != '}' {
				return nil, end, &snbtError{key: "snbt.parser.expected_decimal_numeral", pos: end}
			}
		}
		if num, want, perr, ok := snbtParseNumber(body, suffix, end); ok {
			return &snbtTag{kind: snbtNumber, num: num, end: end, numWant: snbtNumWant(body, suffix)}, end, nil
		} else if perr != nil {
			// A value outside its tag's width fails at the value's
			// end: [128b] ends as number_parse_failure at the closer.
			return nil, end, perr
		} else if want == "INF" {
			// An explicit float suffix that still overflows is not a
			// number at all: [1e999f] ends as infinity_not_allowed.
			return nil, end, &snbtError{key: "snbt.parser.infinity_not_allowed", pos: end}
		} else if want != "" {
			return nil, end, snbtLit(end, want)
		}
	}
	if xpos >= 0 {
		// A zero-led run with a separator and no digit after it
		// wants the hex marker: [0_] ends wanting "x|X" past the
		// zero.
		return nil, xpos, snbtLit(xpos, "x|X")
	}
	// A sign or dot that starts no number still commits, but what it
	// commits to depends on what follows it: a letter or digit
	// anywhere in the word makes it a word (-abc, -.x, -..x and -..5
	// all end wanting "("), while a bare sign, a lone dot, and a sign
	// or dot left hanging before the closer want a decimal numeral
	// past what the number reader took (-. and +. end past the dot).
	if i < len(s) && (s[i] == '+' || s[i] == '-' || s[i] == '.') {
		j := i + 1
		if j >= len(s) || !isWordLetter(s[j]) {
			k := j
			for k < len(s) && isBareChar(s[k]) {
				if isWordLetter(s[k]) || s[k] >= '0' && s[k] <= '9' {
					goto word
				}
				k++
			}
			if j < len(s) && s[j] == '.' {
				return nil, j + 1, &snbtError{key: "snbt.parser.expected_decimal_numeral", pos: j + 1}
			}
			return nil, j, &snbtError{key: "snbt.parser.expected_decimal_numeral", pos: j}
		}
	}
word:
	return snbtWordFallback(s, i)
}

// snbtWordFallback reads the maximal bare word at i: true and false
// read as bytes but trailer like words, while other letter-led words
// are strings. A word opened by a sign, a dot or a digit is not a
// value the reader finishes: [-abc] and [007s] both end wanting "("
// past themselves, while [abc] reads as a string.
func snbtWordFallback(s string, i int) (*snbtTag, int, *snbtError) {
	j := i
	for j < len(s) && isBareChar(s[j]) {
		j++
	}
	if s[i] == '+' || s[i] == '-' || s[i] == '.' || (s[i] >= '0' && s[i] <= '9') {
		return nil, j, snbtLit(j, "(")
	}
	return snbtBareWord(s[i:j], j)
}

// snbtPlainHexHead reports whether a hex number opens at i: a sign and
// a single zero with an x after it. The probes only pinned the empty
// and word edges ([0x] wants a hex numeral, [0xg] reads as a word);
// values below follow the array hex rules the probes did pin.
func snbtPlainHexHead(s string, i int) (xj int, ok bool) {
	j := i
	if j < len(s) && (s[j] == '+' || s[j] == '-') {
		j++
	}
	if j+1 >= len(s) || s[j] != '0' || (s[j+1] != 'x' && s[j+1] != 'X') {
		return 0, false
	}
	return j + 1, true
}

// parseSNBTHex reads the hex number at i whose x sits at xj. It reports
// done when the hex path owns the word: a value, a failure, or a hex
// numeral wanted past an empty 0x. A 0x followed by a bare word
// character is not hex at all ([0xg] reads as a word), which comes back
// as not done for the word fallback - as does a separator with no
// digit after it ([0x1_] reads as a word too, probe pending).
func parseSNBTHex(s string, i, xj int) (tag *snbtTag, next int, herr *snbtError, done bool) {
	ax := xj + 1
	h, hend, hok, htrail := snbtHexDigits(s, xj)
	if !hok {
		if ax < len(s) && isBareChar(s[ax]) {
			return nil, 0, nil, false
		}
		return nil, ax, &snbtError{key: "snbt.parser.expected_hex_numeral", pos: ax}, true
	}
	if htrail {
		return nil, 0, nil, false
	}
	if s[i] == '-' {
		return nil, hend, snbtLit(hend, "u|U"), true
	}
	u, uerr := strconv.ParseUint(h, 16, 32)
	if uerr != nil {
		return nil, hend, snbtLit(hend, "u|U"), true
	}
	n := int64(int32(u))
	if hend < len(s) {
		switch s[hend] {
		case 's', 'S':
			v := int64(int16(n))
			return &snbtTag{kind: snbtNumber, num: strconv.FormatInt(v, 10) + "s", end: hend + 1, numWant: "b|B"}, hend + 1, nil, true
		case 'l', 'L':
			return &snbtTag{kind: snbtNumber, num: strconv.FormatInt(n, 10) + "L", end: hend + 1, numWant: ","}, hend + 1, nil, true
		case 'i', 'I':
			return &snbtTag{kind: snbtNumber, num: strconv.FormatInt(n, 10), end: hend + 1, numWant: ","}, hend + 1, nil, true
		}
	}
	return &snbtTag{kind: snbtNumber, num: strconv.FormatInt(n, 10), end: hend, numWant: "u|U"}, hend, nil, true
}

// isWordLetter reports whether c opens a word the reader finishes:
// after a committed sign a letter runs to the word's end wanting "(".
func isWordLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

// snbtNumberToken matches a number at i: a sign, digit runs with at
// most one dot (or a leading dot with digits), an optional exponent,
// and an optional single suffix letter. Underscores may separate
// digits: [1_2] reads 12, [1__2] too. It reports the body with
// separators stripped, the suffix, and the raw end offset. The
// exponent marker is always taken: 1e without digits still ends past
// the e, and the caller reports that. A separator with no digit after
// it ends the number: [1_] is no number at all (ok is false), and a
// zero-led run there ends wanting "x|X" past the zero, which comes
// back as xpos.
func snbtNumberToken(s string, i int) (body string, suffix byte, end int, ok bool, xpos int) {
	xpos = -1
	j := i
	if j < len(s) && (s[j] == '+' || s[j] == '-') {
		j++
	}
	intStart := j
	j, intDigits, intTrail := snbtDigitRun(s, j)
	intEnd := j
	fracStart := -1
	if intDigits > 0 {
		if j < len(s) && s[j] == '.' && !intTrail {
			fracStart = j + 1
			j++
			var fracTrail bool
			j, _, fracTrail = snbtDigitRun(s, j)
			intTrail = fracTrail
		}
	} else {
		if j >= len(s) || s[j] != '.' || j+1 >= len(s) || s[j+1] < '0' || s[j+1] > '9' {
			return "", 0, i, false, -1
		}
		fracStart = j + 1
		j++
		var fracTrail bool
		j, _, fracTrail = snbtDigitRun(s, j)
		intTrail = fracTrail
	}
	expStart := -1
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') && !intTrail {
		expStart = j
		k := j + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		var expTrail bool
		k, _, expTrail = snbtDigitRun(s, k)
		intTrail = expTrail
		// The marker commits even with no digits after it: 1e ends
		// past the e, 1e+ past the sign.
		j = k
	}
	stripped := snbtStripSeps(s[intStart:intEnd])
	if fracStart >= 0 {
		fracEnd := j
		if expStart >= 0 {
			fracEnd = expStart
		}
		stripped += "." + snbtStripSeps(s[fracStart:fracEnd])
	}
	if expStart >= 0 {
		stripped += snbtStripSeps(s[expStart:j])
	}
	if intTrail {
		// A separator with no digit after it ends the number: [1_]
		// is no number, while a zero-led int run wants the hex
		// marker past its zero instead ([0_] ends wanting "x|X"
		// there). A trailing separator past the dot is always a
		// word: [.0_] ends wanting "(".
		firstDigit := intStart
		if intDigits == 0 {
			firstDigit = fracStart
		}
		if intDigits > 0 && firstDigit < len(s) && s[firstDigit] == '0' {
			return "", 0, firstDigit + 1, false, firstDigit + 1
		}
		return "", 0, i, false, -1
	}
	body = s[i:intStart] + stripped
	// An int suffix fits any integer; on a dotted or exponent double
	// only a float suffix still fits: 1.5s ends past the 5, leaving
	// the s for the trailer. A bare exponent marker takes no suffix:
	// 1ef ends wanting "+" at the f, not past it. The probes pinned a
	// plain i suffix as an int ([1i] reads 1); doubles keep rejecting
	// it ([1.5i] ends wanting "e|E" at the i, probe pending).
	if j < len(s) && strings.IndexByte("bBsSlLfFdDiI", s[j]) >= 0 && snbtBadExp(body, j) == nil {
		dd := strings.ContainsAny(body, ".eE")
		if !dd || s[j] == 'f' || s[j] == 'F' || s[j] == 'd' || s[j] == 'D' {
			suffix, j = s[j], j+1
		}
	}
	return body, suffix, j, true, -1
}

// snbtDigitRun scans digits with underscore separators at j, reporting
// the offset past them, how many digits they held, and whether a
// separator with no digit after it ends the run. A leading separator
// is not one: [_1] scans no digits.
func snbtDigitRun(s string, j int) (int, int, bool) {
	digits := 0
	trail := false
	for j < len(s) {
		c := s[j]
		if c >= '0' && c <= '9' {
			j++
			digits++
			trail = false
			continue
		}
		if c == '_' && digits > 0 {
			j++
			trail = true
			continue
		}
		break
	}
	return j, digits, trail
}

// snbtStripSeps drops the underscore separators from a digit run.
func snbtStripSeps(run string) string {
	if strings.IndexByte(run, '_') < 0 {
		return run
	}
	var b strings.Builder
	for i := 0; i < len(run); i++ {
		if run[i] != '_' {
			b.WriteByte(run[i])
		}
	}
	return b.String()
}

// snbtNumWant reports what the reader reaches for after a complete
// number token: an integer wants its dot, a dotted double its exponent
// marker, an exponent double a float suffix, a complete suffixed
// number the separator - except a short, which wants a byte.
func snbtNumWant(body string, suffix byte) string {
	if suffix == 0 {
		if strings.ContainsAny(body, "eE") {
			return "f|F"
		}
		if strings.Contains(body, ".") {
			return "e|E"
		}
		return "."
	}
	if suffix == 's' || suffix == 'S' {
		return "b|B"
	}
	return ","
}

// snbtBadExp reports a committed exponent marker with no digits: 1e
// ends wanting the sign, 1e+ wanting a decimal numeral past the sign.
func snbtBadExp(body string, end int) *snbtError {
	n := len(body)
	if n >= 1 && (body[n-1] == 'e' || body[n-1] == 'E') {
		return snbtLit(end, "+")
	}
	if n >= 2 && (body[n-2] == 'e' || body[n-2] == 'E') &&
		(body[n-1] == '+' || body[n-1] == '-') {
		return &snbtError{key: "snbt.parser.expected_decimal_numeral", pos: end}
	}
	return nil
}

// snbtBadZero reports the leading zeroes the real server does not
// take: 007 fails where 0, 0.5 and even 00.5 pass.
func snbtBadZero(body string) bool {
	intPart := body
	if strings.HasPrefix(intPart, "+") || strings.HasPrefix(intPart, "-") {
		intPart = intPart[1:]
	}
	if dot := strings.IndexAny(intPart, ".eE"); dot >= 0 {
		return false
	}
	return len(intPart) > 1 && strings.HasPrefix(intPart, "0")
}

// snbtBareWord classifies a non-numeric word: true and false read as
// bytes but trailer like words, ending wanting "(" past themselves.
func snbtBareWord(w string, end int) (*snbtTag, int, *snbtError) {
	switch w {
	case "true":
		return &snbtTag{kind: snbtNumber, num: "1b", end: end, numWant: "("}, end, nil
	case "false":
		return &snbtTag{kind: snbtNumber, num: "0b", end: end, numWant: "("}, end, nil
	}
	return &snbtTag{kind: snbtString, str: w, end: end}, end, nil
}

// snbtParseNumber canonicalizes a numeric word: integers print bare,
// doubles carry their decimal point and a d, bytes, shorts, longs and
// floats their suffix in the case the real server prints (1b, 1s, 2L,
// 1.5d). Words no number parser takes are not numbers. A double that
// overflows wants a float suffix instead: [1e999] ends wanting "f|F".
// A value outside its tag's width comes back as perr: [128b] ends as
// number_parse_failure past the token.
func snbtParseNumber(w string, suffix byte, end int) (num, want string, perr *snbtError, ok bool) {
	body := w
	if n := len(w); n > 1 {
		if c := w[n-1]; strings.IndexByte("bBsSlLfFdD", c) >= 0 {
			body, suffix = w[:n-1], c
		}
	}
	if body == "" {
		return "", "", nil, false
	}
	isDouble := strings.ContainsAny(body, ".eE")
	switch suffix {
	case 'b', 'B':
		// Bytes parse as an int first, then check their width:
		// [9999999999b] ends naming its digits, [128b] ends as
		// Value out of range. Value:"128" Radix:10.
		if isDouble {
			return "", "", nil, false
		}
		if _, err := strconv.ParseInt(body, 10, 32); err != nil {
			perr := snbtNumParseFail(snbtStripSign(body), end)
			return "", "", perr, false
		}
		n, err := strconv.ParseInt(body, 10, 8)
		if err != nil {
			perr := snbtOutOfRange(body)
			perr.pos = end
			return "", "", perr, false
		}
		return strconv.FormatInt(n, 10) + "b", "", nil, true
	case 's', 'S':
		// Guess (short-array probes pending): shorts check their
		// width where bytes fail - a short outside its 16 bits ends
		// wanting a byte, [32768s] ends wanting "b|B" at the closer.
		if isDouble {
			return "", "", nil, false
		}
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil || n < -32768 || n > 32767 {
			return "", "b|B", nil, false
		}
		return strconv.FormatInt(n, 10) + "s", "", nil, true
	case 'l', 'L':
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil || isDouble {
			if err != nil {
				// Guess (sign probes pending): a long outside its
				// 64 bits fails naming its digits, like the arrays.
				return "", "", snbtNumParseFail(snbtStripSign(body), end), false
			}
			return "", "", nil, false
		}
		return strconv.FormatInt(n, 10) + "L", "", nil, true
	case 'i', 'I':
		// An int suffix reads a 32-bit int: [1i] reads 1, and
		// [2147483648i] ends naming its digits.
		if isDouble {
			return "", "", nil, false
		}
		n, err := strconv.ParseInt(body, 10, 32)
		if err != nil {
			return "", "", snbtNumParseFail(snbtStripSign(body), end), false
		}
		return strconv.FormatInt(n, 10), "", nil, true
	case 'f', 'F':
		num, want, ok := snbtFloat(body, 32, "f", true)
		return num, want, nil, ok
	case 'd', 'D':
		num, want, ok := snbtFloat(body, 64, "d", true)
		return num, want, nil, ok
	}
	if isDouble {
		num, want, ok := snbtFloat(body, 64, "d", false)
		return num, want, nil, ok
	}
	// Plain integers are 32-bit: 2147483648 overflows to a reader
	// still wanting its dot, [2147483648] ends wanting "." at the
	// closer.
	n, err := strconv.ParseInt(body, 10, 32)
	if err != nil {
		return "", ".", nil, false
	}
	return strconv.FormatInt(n, 10), "", nil, true
}

// snbtOutOfRange reports a value outside its tag's width, with the
// message the real server carries: Value out of range.
// Value:"128" Radix:10. Callers position it at the value's end.
func snbtOutOfRange(body string) *snbtError {
	return &snbtError{key: "snbt.parser.number_parse_failure", with: []any{`Value out of range. Value:"` + body + `" Radix:10`}}
}

// snbtFloat canonicalizes a decimal word through a float of the tag's
// width, printed the way Java's Double and Float toString print: 2.0
// reads back as 2.0d, [1e-320] as 1.0E-320d. A double that overflows
// wants a float suffix instead ([1e999] ends wanting "f|F"), but an
// explicit suffix that still overflows is not a number at all: [1e999f]
// ends as infinity_not_allowed.
func snbtFloat(body string, bitSize int, suffix string, explicit bool) (num, want string, ok bool) {
	f, err := strconv.ParseFloat(body, bitSize)
	if math.IsInf(f, 0) {
		if explicit {
			return "", "INF", false
		}
		return "", "f|F", false
	}
	if err != nil {
		return "", "", false
	}
	return snbtJavaFloat(f, bitSize) + suffix, "", true
}

// snbtJavaFloat prints f the way Java's Double and Float toString do.
// The shortest digits that parse back are not always what it prints:
// 5e-324 parses to the least denormal but reads back as 4.9E-324d,
// while 1e-320 reads back as 1.0E-320d. What it prints is the shortest
// rounding of the exact value with at least two significant digits
// that reads back: rounding the least denormal to one digit gives
// 5E-324, which parses back but is not what it prints, while two
// digits give 4.9E-324. The search below rounds the exact decimal
// expansion - from math/big, which is exact for binary floats - to n
// digits for n upward from two, renders the rounding in Java's
// notation, and takes the first that parses back to f and re-derives
// itself. Past the cap a value falls back to Go's shortest digits in
// Java's notation; the probes have not caught that path firing.
func snbtJavaFloat(f float64, bitSize int) string {
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	neg := math.Signbit(f)
	digits, exp := snbtExactDigits(f)
	maxN := 20
	if bitSize == 32 {
		maxN = 12
	}
	for n := 2; n <= maxN; n++ {
		d, e := snbtRoundSig(digits, exp, n)
		c := snbtRenderJava(d, e, neg)
		if g, err := strconv.ParseFloat(c, bitSize); err != nil || g != f {
			continue
		}
		rd, re := snbtRoundSig(digits, exp, snbtSigCount(c))
		if snbtRenderJava(rd, re, neg) == c {
			return c
		}
	}
	e := strconv.FormatFloat(f, 'e', -1, bitSize)
	return snbtRenderGoShortest(e, neg)
}

// snbtExactDigits returns the exact decimal significant digits of a
// nonzero finite float and the decimal exponent of its first digit: a
// binary fraction always terminates in decimal, and 1200 fraction
// digits hold the longest expansion one needs.
func snbtExactDigits(f float64) (digits string, exp int) {
	r := new(big.Rat).SetFloat64(math.Abs(f))
	s := r.FloatString(1200)
	dot := strings.IndexByte(s, '.')
	intPart, fracPart := s[:dot], s[dot+1:]
	intPart = strings.TrimLeft(intPart, "0")
	if intPart != "" {
		digits = strings.TrimRight(intPart+fracPart, "0")
		return digits, len(intPart) - 1
	}
	k := 0
	for k < len(fracPart) && fracPart[k] == '0' {
		k++
	}
	digits = strings.TrimRight(fracPart[k:], "0")
	return digits, -(k + 1)
}

// snbtRoundSig rounds exact significant digits with decimal exponent
// exp to n digits, half up: a carry past the front pushes the exponent
// one up.
func snbtRoundSig(digits string, exp, n int) (string, int) {
	if n >= len(digits) {
		return digits, exp
	}
	pre, next := digits[:n], digits[n]
	if next < '5' {
		return pre, exp
	}
	b := []byte(pre)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != '9' {
			b[i]++
			return string(b), exp
		}
		b[i] = '0'
	}
	return "1", exp + 1
}

// snbtSigCount counts the significant digits in a rendered mantissa:
// the digits past the point without trailing zeroes.
func snbtSigCount(c string) int {
	m := c
	if strings.HasPrefix(m, "-") {
		m = m[1:]
	}
	if i := strings.IndexAny(m, "Ee"); i >= 0 {
		m = m[:i]
	}
	m = strings.Replace(m, ".", "", 1)
	m = strings.TrimLeft(m, "0")
	m = strings.TrimRight(m, "0")
	if m == "" {
		return 1
	}
	return len(m)
}

// snbtRenderJava renders significant digits with decimal exponent exp
// in Java's notation: plain between 1e-3 and 1e7, scientific outside,
// with trailing fraction zeroes stripped past one digit.
func snbtRenderJava(digits string, exp int, neg bool) string {
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		digits = "0"
	}
	var out string
	switch {
	case exp < -3 || exp >= 7:
		rest := digits[1:]
		if rest == "" {
			rest = "0"
		}
		out = digits[:1] + "." + rest + "E" + strconv.Itoa(exp)
	case exp+1 <= 0:
		out = "0." + strings.Repeat("0", -exp-1) + digits
	case exp+1 >= len(digits):
		out = digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
	default:
		out = digits[:exp+1] + "." + digits[exp+1:]
	}
	if i := strings.IndexByte(out, 'E'); i < 0 {
		if dot := strings.IndexByte(out, '.'); dot >= 0 {
			out = strings.TrimRight(out, "0")
			if strings.HasSuffix(out, ".") {
				out += "0"
			}
		}
	} else {
		head, tail := out[:i], out[i:]
		if dot := strings.IndexByte(head, '.'); dot >= 0 {
			head = strings.TrimRight(head, "0")
			if strings.HasSuffix(head, ".") {
				head += "0"
			}
		}
		out = head + tail
	}
	if neg {
		return "-" + out
	}
	return out
}

// snbtRenderGoShortest renders Go's shortest scientific digits in
// Java's notation: the fallback when the rounding search exhausts its
// cap.
func snbtRenderGoShortest(e string, neg bool) string {
	if strings.HasPrefix(e, "-") {
		e = e[1:]
	}
	ei := strings.IndexByte(e, 'e')
	mant, expStr := e[:ei], e[ei+1:]
	exp, _ := strconv.Atoi(expStr)
	return snbtRenderJava(strings.Replace(mant, ".", "", 1), exp, neg)
}

// parseSNBTQuoted reads a quoted string at i, handling the escapes.
func parseSNBTQuoted(s string, i int) (string, int, bool) {
	q := s[i]
	var b strings.Builder
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if c == q {
			return b.String(), j + 1, true
		}
		if c == '\\' && j+1 < len(s) {
			j++
			switch s[j] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'u':
				if j+4 < len(s) {
					if n, err := strconv.ParseUint(s[j+1:j+5], 16, 32); err == nil {
						b.WriteRune(rune(n))
						j += 4
						break
					}
				}
				b.WriteByte('u')
			default:
				b.WriteByte(s[j])
			}
			continue
		}
		b.WriteByte(c)
	}
	return "", i, false
}

// parseSNBTCompound reads a compound at i; s[i] is '{'.
func parseSNBTCompound(s string, i int) (*snbtTag, int, *snbtError) {
	tag := &snbtTag{kind: snbtCompound, vals: map[string]*snbtTag{}}
	i++
	if i < len(s) && s[i] == '}' {
		tag.end = i + 1
		return tag, i + 1, nil
	}
	for {
		var key string
		if i < len(s) && (s[i] == '"' || s[i] == '\'') {
			k, next, ok := parseSNBTQuoted(s, i)
			if !ok {
				return nil, i, &snbtError{key: "snbt.parser.invalid_string_contents", pos: len(s)}
			}
			key, i = k, next
		} else {
			j := i
			for j < len(s) && isBareChar(s[j]) {
				j++
			}
			if j == i {
				// No key where one starts: {a:1, ends wanting a quote.
				return nil, i, snbtLit(i, `"`)
			}
			key, i = s[i:j], j
		}
		if i >= len(s) || s[i] != ':' {
			return nil, i, snbtLit(i, ":")
		}
		i++
		val, next, lerr := parseSNBTTag(s, i)
		if lerr != nil {
			return nil, next, lerr
		}
		tag.vals[key] = val
		i = next
		if i < len(s) && s[i] == ',' {
			i++
			// A comma before the closer is tolerated: {a:1,} decodes
			// the {a:1} part.
			if i < len(s) && s[i] == '}' {
				tag.end = i + 1
				return tag, i + 1, nil
			}
			continue
		}
		if i < len(s) && s[i] == '}' {
			tag.end = i + 1
			return tag, i + 1, nil
		}
		// After a complete value the reader reaches for more of it
		// first: the want depends on what it read, a number reaching
		// for "." ({a:1] and [1,2 both end there), a bare string for
		// "(" ([b;1] and {function:minecraft:x} both end there). A
		// compound or list reaching past its bracket is a guess.
		if terr := snbtTrailer(s, i, val, '}'); terr != nil {
			return nil, i, terr
		}
	}
}

// snbtTrailer reports what follows a complete value mid-structure:
// nothing when a separator or the closer follows, else the
// type-based want: a number reaches for more of itself, a bare word
// for "(", a quoted string, compound or list for ",".
func snbtTrailer(s string, i int, val *snbtTag, closer byte) *snbtError {
	if i < len(s) && (s[i] == ',' || s[i] == closer) {
		return nil
	}
	if val.kind == snbtNumber {
		want := val.numWant
		if want == "" {
			want = "."
		}
		return snbtLit(i, want)
	}
	if val.kind == snbtString && !val.quoted {
		return snbtLit(i, "(")
	}
	return snbtLit(i, ",")
}

// parseSNBTList reads a list or typed array at i; s[i] is '['.
func parseSNBTList(s string, i int) (*snbtTag, int, *snbtError) {
	tag := &snbtTag{kind: snbtList}
	i++
	if i < len(s) && s[i] == ']' {
		tag.end = i + 1
		return tag, i + 1, nil
	}
	var arrayType byte
	if i+1 < len(s) && s[i+1] == ';' && (s[i] == 'B' || s[i] == 'I' || s[i] == 'L') {
		arrayType = s[i]
		i += 2
	}
	if arrayType != 0 {
		// The list tag carries the array letter in str so a value
		// conversion can rebuild [B;], [I;] or [L;] from the items.
		tag.str = string(arrayType)
		return parseSNBTArray(s, i, arrayType, tag)
	}
	fresh := true
	for {
		if fresh && (i >= len(s) || (s[i] != '{' && s[i] != '[' && s[i] != '"' && s[i] != '\'' && !isBareChar(s[i]))) {
			// Where the first element starts the reader still wants
			// its array marker: "[" alone and "[," both end wanting
			// "B". Past a comma the same spot wants an unquoted
			// string instead ([1,).
			return nil, i, snbtLit(i, "B")
		}
		el, next, lerr := parseSNBTTag(s, i)
		if lerr != nil {
			return nil, next, lerr
		}
		tag.items = append(tag.items, el)
		i = next
		fresh = false
		if i < len(s) && s[i] == ',' {
			i++
			// A comma before the closer is tolerated: [1,] is [1].
			if i < len(s) && s[i] == ']' {
				tag.end = i + 1
				return tag, i + 1, nil
			}
			continue
		}
		if i < len(s) && s[i] == ']' {
			tag.end = i + 1
			return tag, i + 1, nil
		}
		if terr := snbtTrailer(s, i, el, ']'); terr != nil {
			return nil, i, terr
		}
	}
}

// parseSNBTArray reads a typed array's elements at i. Byte and long
// elements are a bare sign with digits and nothing else: [B;1.5] ends
// wanting "u|U" past the 1, [B;x] ends wanting "+" at the x, and [B;+
// ends wanting "0" past the sign. An int-suffix letter of the wrong
// type is its own failure: [B;1s] ends as invalid_array_element_type,
// while other junk stays "u|U" ([B;1f]). Int elements take any
// int-suffixed number for its value: [I;1s] and [I;1b] both read 1. A
// comma before the closer is tolerated there too: [B;1,] is [1b]. Past
// a complete suffixed element the reader wants the separator: [B;1bX]
// ends wanting "," past the suffix.
func parseSNBTArray(s string, i int, arrayType byte, tag *snbtTag) (*snbtTag, int, *snbtError) {
	suffix := ""
	switch arrayType {
	case 'B':
		suffix = "b"
	case 'L':
		suffix = "L"
	}
	for {
		j := i
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		digits := j
		j, _, _ = snbtDigitRun(s, j)
		if j == digits {
			if j == i {
				return nil, i, snbtLit(i, "+")
			}
			return nil, j, snbtLit(j, "0")
		}
		digs := snbtStripSeps(s[digits:j])
		dec := s[i:digits] + digs
		if s[digits] == '0' && ((digits+1 < len(s) && s[digits+1] == '_') || s[j-1] == '_') {
			// A zero-led run with a separator right after the
			// zero or trailing it wants the hex marker: [B;0_1]
			// and [B;00_] both end wanting "x|X" past the zero,
			// while [B;00_1] reads on to a leading zero.
			return nil, i, snbtLit(digits+1, "x|X")
		}
		if j > digits && s[j-1] == '_' {
			// A separator with no digit after it voids a clean
			// element: [B;1_] ends wanting "+" at the element's
			// start (a zero-led run took the hex marker above).
			return nil, i, snbtLit(i, "+")
		}
		if len(digs) > 1 && digs[0] == '0' {
			// Leading zeroes are not allowed in arrays: [B;007]
			// ends as leading_zero_not_allowed at the closer,
			// [B;01b] at the suffix. A sign is not a digit:
			// [B;-0] reads -0 fine.
			return nil, i, &snbtError{key: "snbt.parser.leading_zero_not_allowed", pos: j}
		}
		var sufbyte byte
		if arrayType == 'I' {
			el, next, suf, aerr := parseSNBTIntElement(s, i, j, dec)
			if aerr != nil {
				return nil, next, aerr
			}
			tag.items = append(tag.items, el)
			i, sufbyte = next, suf
		} else {
			if snbtHexHead(s, arrayType, i, j) {
				h, next, ok, htrail := snbtHexDigits(s, j)
				if !ok {
					if j+1 < len(s) && s[j+1] == '_' {
						// A separator where the first hex digit
						// goes is its own error: [B;0x_1] ends
						// wanting underscore_not_allowed at the
						// separator.
						return nil, i, &snbtError{key: "snbt.parser.underscore_not_allowed", pos: j + 1}
					}
					// 0x with no hex digits after it wants a hex
					// numeral past the x: [B;0x] ends wanting one
					// at the closer, [B;0x,1] at the comma.
					return nil, i, &snbtError{key: "snbt.parser.expected_hex_numeral", pos: j + 1}
				}
				if htrail {
					// A separator with no hex digit after it is
					// its own error at the run's start: [B;0x1_]
					// and [B;0x12_] both end wanting
					// underscore_not_allowed past the x.
					return nil, i, &snbtError{key: "snbt.parser.underscore_not_allowed", pos: j + 1}
				}
				if dec == "-0" {
					// A negative hex element is rejected: [B;-0x1]
					// ends as expected_non_negative_number at the
					// element's end.
					return nil, i, &snbtError{key: "snbt.parser.expected_non_negative_number", pos: len(s)}
				}
				// A zero-x element reads hex: [B;0x1] is [1b] echoed
				// back decimal, [I;0x10] is [16], [L;0x10] is [16L].
				v, verr := snbtHexValue(h, arrayType)
				if verr != nil {
					verr.pos = len(s)
					return nil, i, verr
				}
				dec, j = v, next
			} else {
				if arrayType == 'L' && dec == "-0" && j < len(s) && (s[j] == 'x' || s[j] == 'X') {
					// Longs peek at the hex after a -0x: empty wants
					// a hex numeral past it ([L;-0x]), a separator
					// is its own error ([L;-0x_1]), and digits are
					// rejected as negative ([L;-0x1] ends as
					// expected_non_negative_number at the token's
					// end, [L;-0x1_] as underscore_not_allowed
					// past the x).
					_, _, lok, ltrail := snbtHexDigits(s, j)
					if !lok {
						if j+1 < len(s) && s[j+1] == '_' {
							return nil, i, &snbtError{key: "snbt.parser.underscore_not_allowed", pos: j + 1}
						}
						return nil, i, &snbtError{key: "snbt.parser.expected_hex_numeral", pos: j + 1}
					}
					if ltrail {
						return nil, i, &snbtError{key: "snbt.parser.underscore_not_allowed", pos: j + 1}
					}
					return nil, i, &snbtError{key: "snbt.parser.expected_non_negative_number", pos: len(s)}
				}
				if j < len(s) && strings.IndexByte("bBsSlLiI", s[j]) >= 0 && !snbtArraySuffix(arrayType, s[j]) &&
					!snbtArrayCoerce(arrayType, s[j]) {
					return nil, i, &snbtError{key: "snbt.parser.invalid_array_element_type", pos: len(s)}
				}
				if arrayType == 'B' {
					// Byte elements are 8-bit: [B;128] ends as
					// number_parse_failure at the element's end.
					n, berr := strconv.ParseInt(dec, 10, 8)
					if berr != nil {
						// Digits no int takes fail naming them:
						// [B;99999999999999999999999] ends as For
						// input string: "999...". A plus is not
						// named: [B;+129] ends as Value out of
						// range. Value:"129".
						if _, ierr := strconv.ParseInt(dec, 10, 32); ierr != nil {
							return nil, i, snbtNumParseFail(snbtStripSign(dec), len(s))
						}
						perr := snbtOutOfRange(snbtStripSign(dec))
						perr.pos = len(s)
						return nil, i, perr
					}
					dec = strconv.FormatInt(n, 10)
				} else {
					// Long elements parse at their suffix's width and
					// print long: [L;100b] is [100L], [L;128b] ends
					// as Value out of range. Value:"128" Radix:10,
					// and [L;9999999999s] ends naming its digits. A
					// long element that overflows fails naming its
					// magnitude without its plus: [B;+999...] names
					// 999....
					bits := 64
					if j < len(s) {
						switch s[j] {
						case 'b', 'B':
							bits = 8
						case 's', 'S':
							bits = 16
						case 'i', 'I':
							bits = 32
						}
					}
					n, err := strconv.ParseInt(dec, 10, bits)
					if err != nil {
						if _, ierr := strconv.ParseInt(dec, 10, 32); ierr != nil || bits == 64 {
							return nil, i, snbtNumParseFail(snbtStripSign(dec), len(s))
						}
						perr := snbtOutOfRange(snbtStripSign(dec))
						perr.pos = len(s)
						return nil, i, perr
					}
					dec = strconv.FormatInt(n, 10)
				}
			}
			num := dec + suffix
			if j < len(s) && (snbtArraySuffix(arrayType, s[j]) || snbtArrayCoerce(arrayType, s[j])) {
				j++
				sufbyte = s[j-1]
			}
			tag.items = append(tag.items, &snbtTag{kind: snbtNumber, num: num, end: j})
			i = j
		}
		if i < len(s) && s[i] == ',' {
			i++
			if i < len(s) && s[i] == ']' {
				tag.end = i + 1
				return tag, i + 1, nil
			}
			continue
		}
		if i < len(s) && s[i] == ']' {
			tag.end = i + 1
			return tag, i + 1, nil
		}
		// Past a complete suffixed element the reader wants what the
		// suffix wants: "," except after a short, which wants "b|B".
		if sufbyte != 0 {
			if sufbyte == 's' || sufbyte == 'S' {
				return nil, i, snbtLit(i, "b|B")
			}
			return nil, i, snbtLit(i, ",")
		}
		return nil, i, snbtLit(i, "u|U")
	}
}

// parseSNBTIntElement reads one int-array element: the stripped digits
// dec scanned at s[i:j] with an optional int-family suffix letter,
// valued as a bare int. A zero-x opens hex like in the other arrays:
// [I;0x10] is [16], [I;0xFFFFFFFF] is [-1], and overlong hex fails
// naming its digits ([I;0x100000000] names 100000000). A byte or short
// suffix reads at its own width: [I;32768s] ends as Value out of
// range. Value:"32768" Radix:10, while digits no int takes fail naming
// them ([I;9999999999s] names 9999999999). A long suffix does not fit:
// [I;1L] ends as invalid_array_element_type. Anything else after the
// digits is a guess: doubles and exponents end wanting "u|U" there.
// Whether overflow messages echo separators is probe-pending.
func parseSNBTIntElement(s string, i, j int, dec string) (*snbtTag, int, byte, *snbtError) {
	if (dec == "0" || dec == "+0" || dec == "-0") && j < len(s) && (s[j] == 'x' || s[j] == 'X') {
		h, next, ok, htrail := snbtHexDigits(s, j)
		if !ok {
			if j+1 < len(s) && s[j+1] == '_' {
				return nil, i, 0, &snbtError{key: "snbt.parser.underscore_not_allowed", pos: j + 1}
			}
			return nil, i, 0, &snbtError{key: "snbt.parser.expected_hex_numeral", pos: j + 1}
		}
		if htrail {
			return nil, i, 0, &snbtError{key: "snbt.parser.underscore_not_allowed", pos: j + 1}
		}
		if dec == "-0" {
			// A negative hex element is rejected: [I;-0x1]
			// ends as expected_non_negative_number at the
			// token's end.
			return nil, i, 0, &snbtError{key: "snbt.parser.expected_non_negative_number", pos: len(s)}
		}
		v, verr := snbtHexValue(h, 'I')
		if verr != nil {
			verr.pos = len(s)
			return nil, i, 0, verr
		}
		dec, j = v, next
	}
	k := j
	var sufbyte byte
	if k < len(s) && strings.IndexByte("bBsSiI", s[k]) >= 0 {
		sufbyte = s[k]
		k++
	} else if k < len(s) && (s[k] == 'l' || s[k] == 'L') {
		return nil, i, 0, &snbtError{key: "snbt.parser.invalid_array_element_type", pos: len(s)}
	}
	bits := 32
	if sufbyte == 'b' || sufbyte == 'B' {
		bits = 8
	} else if sufbyte == 's' || sufbyte == 'S' {
		bits = 16
	}
	n, err := strconv.ParseInt(dec, 10, bits)
	if err != nil {
		if _, ierr := strconv.ParseInt(dec, 10, 32); ierr != nil {
			return nil, i, 0, snbtNumParseFail(snbtStripSign(dec), len(s))
		}
		// A byte or short suffix outside its own width fails as
		// out of range, echoing the digits stripped of a plus:
		// [I;+128b] ends as Value out of range. Value:"128".
		perr := snbtOutOfRange(snbtStripSign(dec))
		perr.pos = len(s)
		return nil, i, 0, perr
	}
	return &snbtTag{kind: snbtNumber, num: strconv.FormatInt(n, 10), end: k}, k, sufbyte, nil
}

// snbtNumParseFail reports digits no long parser takes, with the
// message the real server carries: For input string: "999...".
func snbtNumParseFail(digits string, pos int) *snbtError {
	return &snbtError{key: "snbt.parser.number_parse_failure", pos: pos, with: []any{`For input string: "` + digits + `"`}}
}

// snbtStripSign drops a leading plus, for the overflow messages that
// name a magnitude without it: [B;+999...] names 999.... A minus is
// kept: [I;-2147483649] names -2147483649.
func snbtStripSign(d string) string {
	return strings.TrimPrefix(d, "+")
}

// snbtHexHead reports whether the digits at s[i:j] open a hex
// element: a bare zero with an x after it at j. Longs do not take a
// minus there: [L;-0x1] reads -0 with junk after it.
func snbtHexHead(s string, arrayType byte, i, j int) bool {
	if j >= len(s) || (s[j] != 'x' && s[j] != 'X') {
		return false
	}
	dec := s[i:j]
	if dec == "0" || dec == "+0" {
		return true
	}
	return dec == "-0" && arrayType != 'L'
}

// snbtHexDigits scans the hex digits after a 0x at j, returning them
// with separators stripped and the offset past them, or false when
// none follow. A separator with no digit after it ends the run, which
// comes back as trail: [0x1_] is no hex number.
func snbtHexDigits(s string, j int) (string, int, bool, bool) {
	k := j + 1
	digits := 0
	trail := false
	for k < len(s) {
		c := s[k]
		if strings.IndexByte("0123456789abcdefABCDEF", c) >= 0 {
			k++
			digits++
			trail = false
			continue
		}
		if c == '_' && digits > 0 {
			k++
			trail = true
			continue
		}
		break
	}
	if digits == 0 {
		return "", 0, false, false
	}
	return snbtStripSeps(s[j+1 : k]), k, true, trail
}

// snbtHexValue reads hex digits h as arrayType's width: bytes parse
// signed-long, fail past 255 as out of range: 256, and cast the rest
// ([B;0xFF] is [-1b]); ints parse unsigned ([I;0xFFFFFFFF] is [-1]).
// Longs parse unsigned-long: [L;0xFFFFFFFFFFFFFFFF] is [-1L].
func snbtHexValue(h string, arrayType byte) (string, *snbtError) {
	switch arrayType {
	case 'I':
		// Ints parse unsigned and print signed: [I;0xFFFFFFFF]
		// is [-1]. Overlong hex fails naming the digits as
		// written: String value 100000000 exceeds range of
		// unsigned int.
		n, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return "", &snbtError{key: "snbt.parser.number_parse_failure", with: []any{`String value ` + h + ` exceeds range of unsigned int.`}}
		}
		return strconv.FormatInt(int64(int32(n)), 10), nil
	case 'L':
		// Guess (overflow probe pending): overlong hex fails like
		// the other widths.
		n, err := strconv.ParseUint(h, 16, 64)
		if err != nil {
			return "", &snbtError{key: "snbt.parser.number_parse_failure", with: []any{`String value ` + h + ` exceeds range of unsigned long.`}}
		}
		return strconv.FormatInt(int64(n), 10), nil
	default:
		n, err := strconv.ParseInt(h, 16, 64)
		if err != nil {
			// The digits stay quoted on their own: overlong hex
			// ends as For input string: "ff..." under radix 16.
			return "", &snbtError{key: "snbt.parser.number_parse_failure", with: []any{`For input string: "` + h + `" under radix 16`}}
		}
		if n > 255 {
			return "", &snbtError{key: "snbt.parser.number_parse_failure", with: []any{`out of range: ` + strconv.FormatInt(n, 10)}}
		}
		return strconv.FormatInt(int64(int8(n)), 10), nil
	}
}

// snbtArraySuffix reports whether c is the suffix letter arrayType's
// elements print with: b for a byte array, L for a long one. Ints
// print bare, so no letter fits them.
func snbtArraySuffix(arrayType, c byte) bool {
	switch arrayType {
	case 'B':
		return c == 'b' || c == 'B'
	case 'L':
		return c == 'l' || c == 'L'
	}
	return false
}

// snbtArrayCoerce reports whether c is a narrower int-family suffix the
// array reads for its own value: [L;1s], [L;1b] and [L;1i] all read 1L.
func snbtArrayCoerce(arrayType, c byte) bool {
	return arrayType == 'L' && strings.IndexByte("bBsSiI", c) >= 0
}

// snbtCanon prints a tag the way the real server echoes what it read:
// sorted keys, quoted strings, canonical numbers, no spaces.
func snbtCanon(t *snbtTag) string {
	switch t.kind {
	case snbtString:
		return `"` + snbtEscape(t.str) + `"`
	case snbtNumber:
		return t.num
	case snbtList:
		parts := make([]string, 0, len(t.items))
		for _, el := range t.items {
			parts = append(parts, snbtCanon(el))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		keys := make([]string, 0, len(t.vals))
		for k := range t.vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, snbtCanonKey(k)+":"+snbtCanon(t.vals[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
}

// snbtCanonKey prints a compound key bare when it parses bare.
func snbtCanonKey(k string) string {
	if k == "" {
		return `""`
	}
	for i := 0; i < len(k); i++ {
		if !isBareChar(k[i]) {
			return `"` + snbtEscape(k) + `"`
		}
	}
	return k
}

// snbtEscape escapes a string for the canonical echo.
func snbtEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// snbtNum coerces a numeric tag to float64; non-numbers read 0.
func snbtNum(t *snbtTag) float64 {
	if t == nil || t.kind != snbtNumber {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.TrimRight(t.num, "bslfBSLFdiI"), 64)
	return f
}
