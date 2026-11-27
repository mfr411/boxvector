package web

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Policy rules: a small line-based language compiled to BIRD (valid for BIRD 2 and 3), so a peer
// can get "only accept origin AS65001 and prefix 10.0.0.0/8 le 24" without writing filters.
//
//	only <cond>            reject everything that does NOT match
//	reject <cond>          reject what matches (alias: suppress, deny)
//	med N | prepend N | add-community A:B | add-large-community A:B:C  [when <cond>]
//
// <cond> = [not] term (and|or [not] term)...   (and/or cannot be mixed on one line)
// terms: origin, first-as, path-contains, path-len, path, prefix, community, large-community, ipv4, ipv6
// Rules never "accept" outright, so the later RPKI/bogon/IRR filters still apply.

type polDir int

const (
	polImport polDir = iota
	polExport
)

const (
	polBegin = "# bv:begin (generated from the rules below, edit them in the web UI)"
	polEnd   = "# bv:end"
	polMark  = "# | "
)

func parseASN(s string) (uint32, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToUpper(s), "AS"), 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%q is not a valid ASN", s)
	}
	return uint32(n), nil
}

func asnSet(toks []string) (string, error) {
	if len(toks) == 0 {
		return "", fmt.Errorf("ASN expected")
	}
	var out []string
	for _, t := range toks {
		if a, b, ok := strings.Cut(t, "-"); ok {
			x, err := parseASN(a)
			if err != nil {
				return "", err
			}
			y, err := parseASN(b)
			if err != nil || y < x {
				return "", fmt.Errorf("invalid ASN range %q", t)
			}
			out = append(out, fmt.Sprintf("%d..%d", x, y))
			continue
		}
		x, err := parseASN(t)
		if err != nil {
			return "", err
		}
		out = append(out, strconv.FormatUint(uint64(x), 10))
	}
	return strings.Join(out, ", "), nil
}

func u16(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	return uint16(n), err
}

func parsePrefixes(toks []string) (string, error) {
	var v4, v6 []string
	for i := 0; i < len(toks); i++ {
		p, err := netip.ParsePrefix(toks[i])
		if err != nil || p.Masked() != p {
			return "", fmt.Errorf("%q is not a valid prefix (host bits must be 0)", toks[i])
		}
		max := p.Addr().BitLen()
		ge, le := p.Bits(), p.Bits()
		for i+1 < len(toks) && (toks[i+1] == "ge" || toks[i+1] == "le") {
			if i+2 >= len(toks) {
				return "", fmt.Errorf("%q needs a number", toks[i+1])
			}
			n, err := strconv.Atoi(toks[i+2])
			if err != nil || n < 0 || n > max {
				return "", fmt.Errorf("invalid length %q", toks[i+2])
			}
			if toks[i+1] == "ge" {
				ge = n
				if le < n {
					le = max
				}
			} else {
				le = n
			}
			i += 2
		}
		if ge < p.Bits() || le < ge || le > max {
			return "", fmt.Errorf("length range for %s must satisfy /%d <= ge <= le <= /%d", p, p.Bits(), max)
		}
		e := fmt.Sprintf("%s{%d,%d}", p, ge, le)
		if p.Addr().Is4() {
			v4 = append(v4, e)
		} else {
			v6 = append(v6, e)
		}
	}
	var parts []string
	if len(v4) > 0 {
		parts = append(parts, "(net.type = NET_IP4 && net ~ ["+strings.Join(v4, ", ")+"])")
	}
	if len(v6) > 0 {
		parts = append(parts, "(net.type = NET_IP6 && net ~ ["+strings.Join(v6, ", ")+"])")
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("prefix expected")
	}
	return strings.Join(parts, " || "), nil
}

func parseTerm(t []string) (string, error) {
	if len(t) == 0 {
		return "", fmt.Errorf("condition expected")
	}
	neg := false
	if strings.EqualFold(t[0], "not") {
		neg, t = true, t[1:]
		if len(t) == 0 {
			return "", fmt.Errorf("condition expected after not")
		}
	}
	kw, v := strings.ToLower(t[0]), t[1:]
	var e string
	var err error
	switch kw {
	case "origin", "origin-as":
		var s string
		if s, err = asnSet(v); err == nil {
			e = "bgp_path.last ~ [" + s + "]"
		}
	case "first-as", "neighbor-as":
		var s string
		if s, err = asnSet(v); err == nil {
			e = "bgp_path.first ~ [" + s + "]"
		}
	case "path-contains", "contains":
		if len(v) == 0 {
			return "", fmt.Errorf("ASN expected")
		}
		var l []string
		for _, a := range v {
			n, er := parseASN(a)
			if er != nil {
				return "", er
			}
			l = append(l, fmt.Sprintf("%d ~ bgp_path", n))
		}
		e = "(" + strings.Join(l, " || ") + ")"
	case "path-len", "path-length":
		if len(v) != 2 {
			return "", fmt.Errorf("use: path-len <|<=|>|>=|= N")
		}
		op := map[string]string{"<": "<", "<=": "<=", ">": ">", ">=": ">=", "=": "=", "==": "=", "!=": "!="}[v[0]]
		n, er := strconv.Atoi(v[1])
		if op == "" || er != nil || n < 0 || n > 255 {
			return "", fmt.Errorf("use: path-len <|<=|>|>=|= N (N 0-255)")
		}
		e = fmt.Sprintf("bgp_path.len %s %d", op, n)
	case "path", "path-pattern", "as-path":
		if len(v) == 0 || len(v) > 20 {
			return "", fmt.Errorf("path pattern: 1-20 items of ASN, * (any number of ASNs) or ? (one ASN)")
		}
		var l []string
		for _, a := range v {
			if a == "*" || a == "?" {
				l = append(l, a)
				continue
			}
			n, er := parseASN(a)
			if er != nil {
				return "", er
			}
			l = append(l, strconv.FormatUint(uint64(n), 10))
		}
		e = "bgp_path ~ [= " + strings.Join(l, " ") + " =]"
	case "prefix", "prefixes", "net":
		e, err = parsePrefixes(v)
	case "community", "communities":
		if len(v) == 0 {
			return "", fmt.Errorf("community A:B expected")
		}
		var l []string
		for _, c := range v {
			a, b, ok := strings.Cut(c, ":")
			x, e1 := u16(a)
			y, e2 := u16(b)
			if !ok || e1 != nil || e2 != nil {
				return "", fmt.Errorf("%q is not a community (A:B, 0-65535)", c)
			}
			l = append(l, fmt.Sprintf("(%d,%d) ~ bgp_community", x, y))
		}
		e = "(" + strings.Join(l, " || ") + ")"
	case "large-community", "large-communities":
		if len(v) == 0 {
			return "", fmt.Errorf("large community A:B:C expected")
		}
		var l []string
		for _, c := range v {
			p := strings.Split(c, ":")
			if len(p) != 3 {
				return "", fmt.Errorf("%q is not a large community (A:B:C)", c)
			}
			var n [3]uint64
			for i := range p {
				if n[i], err = strconv.ParseUint(p[i], 10, 32); err != nil {
					return "", fmt.Errorf("%q is not a large community (A:B:C)", c)
				}
			}
			l = append(l, fmt.Sprintf("(%d,%d,%d) ~ bgp_large_community", n[0], n[1], n[2]))
		}
		e = "(" + strings.Join(l, " || ") + ")"
	case "ipv4", "v4":
		e = "net.type = NET_IP4"
	case "ipv6", "v6":
		e = "net.type = NET_IP6"
	default:
		return "", fmt.Errorf("unknown condition %q (origin, first-as, path-contains, path-len, path, prefix, community, large-community, ipv4, ipv6)", t[0])
	}
	if err != nil {
		return "", err
	}
	if neg {
		return "!(" + e + ")", nil
	}
	return "(" + e + ")", nil
}

func isJoin(s string) bool { s = strings.ToLower(s); return s == "and" || s == "or" }

func parseCond(toks []string) (string, error) {
	var terms [][]string
	cur := []string{}
	op := ""
	for _, t := range toks {
		if isJoin(t) {
			l := strings.ToLower(t)
			if op != "" && op != l {
				return "", fmt.Errorf("do not mix and/or on one line, split it into two rules")
			}
			op = l
			terms, cur = append(terms, cur), []string{}
			continue
		}
		cur = append(cur, t)
	}
	terms = append(terms, cur)
	var out []string
	for _, t := range terms {
		e, err := parseTerm(t)
		if err != nil {
			return "", err
		}
		out = append(out, e)
	}
	j := " && "
	if op == "or" {
		j = " || "
	}
	return "(" + strings.Join(out, j) + ")", nil
}

func compileRule(line string, dir polDir) (string, error) {
	line = strings.NewReplacer(",", " ", "\t", " ").Replace(line)
	toks := strings.Fields(line)
	if len(toks) == 0 {
		return "", nil
	}
	if strings.EqualFold(toks[0], "set") {
		toks = toks[1:]
		if len(toks) == 0 {
			return "", fmt.Errorf("rule expected after set")
		}
	}
	verb := strings.ToLower(toks[0])
	rest := toks[1:]
	switch verb {
	case "only", "reject", "suppress", "deny":
		if len(rest) > 0 && (strings.EqualFold(rest[0], "when") || strings.EqualFold(rest[0], "if")) {
			rest = rest[1:]
		}
		c, err := parseCond(rest)
		if err != nil {
			return "", err
		}
		if verb == "only" {
			return "if !" + c + " then reject;", nil
		}
		return "if " + c + " then reject;", nil
	}
	// modifiers, optionally "when <cond>"
	args, cond := rest, ""
	for i, t := range rest {
		if strings.EqualFold(t, "when") || strings.EqualFold(t, "if") {
			c, err := parseCond(rest[i+1:])
			if err != nil {
				return "", err
			}
			args, cond = rest[:i], c
			break
		}
	}
	var act string
	one := func() (string, error) {
		if len(args) != 1 {
			return "", fmt.Errorf("%s takes exactly one value", verb)
		}
		return args[0], nil
	}
	switch verb {
	case "med":
		a, err := one()
		n, e2 := strconv.ParseUint(a, 10, 32)
		if err != nil || e2 != nil {
			return "", fmt.Errorf("med needs a number (0-4294967295)")
		}
		act = fmt.Sprintf("bgp_med = %d;", n)
	case "prepend":
		a, err := one()
		n, e2 := strconv.Atoi(a)
		if err != nil || e2 != nil || n < 1 || n > 10 {
			return "", fmt.Errorf("prepend needs a count between 1 and 10")
		}
		act = strings.Repeat("bgp_path.prepend(ASN); ", n)
		act = strings.TrimSpace(act)
	case "add-community":
		a, err := one()
		x, y, ok := strings.Cut(a, ":")
		p, e1 := u16(x)
		q, e2 := u16(y)
		if err != nil || !ok || e1 != nil || e2 != nil {
			return "", fmt.Errorf("add-community needs A:B")
		}
		act = fmt.Sprintf("bgp_community.add((%d,%d));", p, q)
	case "add-large-community":
		a, err := one()
		p := strings.Split(a, ":")
		if err != nil || len(p) != 3 {
			return "", fmt.Errorf("add-large-community needs A:B:C")
		}
		var n [3]uint64
		for i := range p {
			if n[i], err = strconv.ParseUint(p[i], 10, 32); err != nil {
				return "", fmt.Errorf("add-large-community needs A:B:C")
			}
		}
		act = fmt.Sprintf("bgp_large_community.add((%d,%d,%d));", n[0], n[1], n[2])
	default:
		return "", fmt.Errorf("unknown rule %q (only, reject, med, prepend, add-community, add-large-community)", toks[0])
	}
	if cond == "" {
		return act, nil
	}
	return "if " + cond + " then { " + act + " }", nil
}

// buildPolicyValue joins the compiled rules and the custom BIRD text into the peer key value.
func buildPolicyValue(rules, raw string, dir polDir) (string, string) {
	rules = strings.ReplaceAll(rules, "\r", "")
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r", ""))
	if len(raw) > 8192 {
		return "", "custom BIRD text is limited to 8192 characters."
	}
	var b strings.Builder
	n := 0
	for i, l := range strings.Split(rules, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if n++; n > 50 {
			return "", "at most 50 rules."
		}
		if len(l) > 300 {
			return "", fmt.Sprintf("rule %d is too long (max 300 characters).", i+1)
		}
		code, err := compileRule(l, dir)
		if err != nil {
			return "", fmt.Sprintf("rule %d (%s): %v.", i+1, l, err)
		}
		if b.Len() == 0 {
			b.WriteString(polBegin + "\n")
		}
		b.WriteString(polMark + l + "\n" + code + "\n")
	}
	if b.Len() > 0 {
		b.WriteString(polEnd + "\n")
	}
	if raw != "" {
		b.WriteString(raw + "\n")
	}
	return b.String(), ""
}

// parsePolicyValue splits a stored peer value back into the rule lines and the custom BIRD part.
func parsePolicyValue(v string) (rules, raw string) {
	var r, c []string
	in := false
	for _, l := range strings.Split(strings.ReplaceAll(v, "\r", ""), "\n") {
		switch {
		case strings.HasPrefix(l, "# bv:begin"):
			in = true
		case strings.HasPrefix(l, "# bv:end"):
			in = false
		case in && strings.HasPrefix(l, polMark):
			r = append(r, strings.TrimPrefix(l, polMark))
		case in:
		default:
			c = append(c, l)
		}
	}
	return strings.Join(r, "\n"), strings.TrimSpace(strings.Join(c, "\n"))
}
