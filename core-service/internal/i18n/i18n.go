// Package i18n localizes user-facing API error messages. The language
// comes from the Accept-Language header (Turkish by default); messages
// are looked up by Key in a fixed TR/EN catalog (catalog.go). Error codes
// are not localized: clients branch on the code, the message is for
// people.
package i18n

import (
	"fmt"
	"strconv"
	"strings"
)

// Lang is a supported message language.
type Lang string

const (
	TR Lang = "tr"
	EN Lang = "en"

	// Default is used when the header names no supported language.
	Default = TR
)

// Key identifies one message in the catalog. It is a struct, not a
// string, so a stray string literal passed where a Key is expected does
// not compile. Its id is never sent to clients.
type Key struct{ id string }

func (k Key) String() string { return k.id }

// FromAcceptLanguage picks the supported language with the highest q
// value from an Accept-Language header ("en-US,en;q=0.9,tr;q=0.8").
// Ties go to the earlier entry; region subtags are ignored ("en-GB" is
// EN); q=0, malformed q values and "*" never select a language. An empty
// or unsupported header yields Default.
func FromAcceptLanguage(header string) Lang {
	best, bestQ := Default, 0.0
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		lang, ok := supported(tag)
		if !ok {
			continue
		}
		if q := quality(params); q > bestQ {
			best, bestQ = lang, q
		}
	}
	return best
}

// supported maps a language tag to a Lang by its primary subtag.
func supported(tag string) (Lang, bool) {
	primary, _, _ := strings.Cut(strings.TrimSpace(tag), "-")
	primary, _, _ = strings.Cut(primary, "_")
	switch strings.ToLower(primary) {
	case "tr":
		return TR, true
	case "en":
		return EN, true
	}
	return "", false
}

// quality returns the q parameter (1 when absent, 0 when malformed).
func quality(params string) float64 {
	for _, p := range strings.Split(params, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || q < 0 || q > 1 {
			return 0
		}
		return q
	}
	return 1
}

// Message returns the text for key in lang, formatted with args
// (fmt.Sprintf verbs) when any are given. An unknown key returns the key
// itself, so a missing entry is visible rather than an empty message.
func Message(lang Lang, key Key, args ...any) string {
	t, ok := catalog[key]
	if !ok {
		return key.id
	}
	s := t.tr
	if lang == EN {
		s = t.en
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}
