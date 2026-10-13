package client

import (
	"fmt"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// socialRoot is the root of publication copies. It belongs to a publication,
// not to a user: ownership lives in the sosyal.yayin row, not in the key. That
// is why it is NOT in ownedKinds and /api/files/sign does not sign these keys;
// signed URLs come only from the social endpoints that apply the visibility
// rule.
const socialRoot = "sosyal"

// validPublicationID verifies that a publication id can be a single key
// segment. It cannot contain a dot: it is an identifier, not a file name.
func validPublicationID(pubID string) bool {
	return segmentPattern.MatchString(pubID) && !strings.Contains(pubID, ".")
}

// SocialKey builds the "sosyal/<pubId>/<parts...>" key. Every part goes
// through the same segment rule as UserKey; a part carrying "/" or ".." is
// rejected. Layout:
//
//	sosyal/{pubId}/master.{ext}
//	sosyal/{pubId}/onizleme.mp3
//	sosyal/{pubId}/stems/{stemId}.{ext}
func SocialKey(pubID string, parts ...string) (string, error) {
	if !validPublicationID(pubID) {
		return "", ErrInvalidSegment
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%w: file name missing", ErrInvalidSegment)
	}
	for _, p := range parts {
		if !segmentPattern.MatchString(p) {
			return "", ErrInvalidSegment
		}
	}
	return socialRoot + "/" + pubID + "/" + strings.Join(parts, "/"), nil
}

// SocialPrefix is the "sosyal/<pubId>/" prefix covering all files of a
// publication. It satisfies DeletePrefix's three-segment rule.
func SocialPrefix(pubID string) (string, error) {
	if !validPublicationID(pubID) {
		return "", ErrInvalidSegment
	}
	return socialRoot + "/" + pubID + "/", nil
}

// ResolveOwnedKeyOrURL is the variant of ResolveOwnedKey that also accepts a
// bare key. The fileUrl of render and master results can be a full URL or (on
// mock/dev paths and in old records) a bare key; KeyFromURL rejects a
// host-less string with ErrInvalidURL.
//
// If the bare key does not start with a known kind in the form
// "<kind>/<userId>/..." it returns ErrInvalidURL; if it is under someone
// else's prefix it returns ErrKeyNotOwned.
func ResolveOwnedKeyOrURL(raw, userID string, cfg *config.R2Config) (string, error) {
	if strings.Contains(raw, "://") {
		return ResolveOwnedKey(raw, userID, cfg)
	}
	segments := strings.Split(raw, "/")
	if len(segments) < 3 || !ownedKinds[segments[0]] {
		return "", fmt.Errorf("%w: neither a key nor a URL", ErrInvalidURL)
	}
	if err := VerifyKeyOwner(raw, userID); err != nil {
		return "", err
	}
	return raw, nil
}
