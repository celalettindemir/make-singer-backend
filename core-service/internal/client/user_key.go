package client

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// ErrInvalidSegment: the value cannot be a key segment coming from a client.
// The message carries no value; the error can land in the Redis job record
// and in the response.
var ErrInvalidSegment = errors.New("invalid key segment")

// ErrInvalidURL: the URL the client sent could not be parsed or belongs to an
// unknown host. Endpoints map it to 400. The message carries no URL value.
var ErrInvalidURL = errors.New("invalid URL")

// ErrKeyNotOwned: the object is outside the requesting user's prefix. Endpoints
// map it to 404; a 403 would confirm that the object exists.
var ErrKeyNotOwned = errors.New("object does not belong to this user")

// ownedKinds: for keys whose first segment is one of these, the second
// segment is always the owner's userId.
var ownedKinds = map[string]bool{
	"vocals":   true,
	"masters":  true,
	"stems":    true,
	"exports":  true,
	"previews": true,
}

// segmentPattern: cannot carry "/" or "..", starts with a letter or digit, and
// allows a single extension dot.
var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}(\.[a-z0-9]{1,5})?$`)

// UserKey builds the "<kind>/<userId>/<parts...>" key. The last part is the
// file name with an extension. Ownership is therefore structural: nobody can
// write outside their own prefix and there is no separate table to check.
func UserKey(kind, userID string, parts ...string) (string, error) {
	if !ownedKinds[kind] {
		return "", fmt.Errorf("unknown key kind: %s", kind)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%w: file name missing", ErrInvalidSegment)
	}
	all := append([]string{userID}, parts...)
	for _, p := range all {
		if !segmentPattern.MatchString(p) {
			return "", ErrInvalidSegment
		}
	}
	return kind + "/" + strings.Join(all, "/"), nil
}

// VerifyKeyOwner verifies that the key is under userID's prefix. Keys in the
// old (prefix-less) layout are rejected too.
func VerifyKeyOwner(key, userID string) error {
	segments := strings.Split(key, "/")
	if userID == "" || len(segments) < 3 || !ownedKinds[segments[0]] || segments[1] != userID {
		return ErrKeyNotOwned
	}
	for _, p := range segments[1:] {
		if !segmentPattern.MatchString(p) {
			return ErrKeyNotOwned
		}
	}
	return nil
}

// ResolveOwnedKey converts a URL coming from a client into a key and verifies
// that the key belongs to the requesting user. The worker and audio-service
// READ this key, so an unchecked URL would make them read someone else's file.
func ResolveOwnedKey(rawURL, userID string, cfg *config.R2Config) (string, error) {
	key, err := KeyFromURL(rawURL, cfg)
	if err != nil {
		return "", err
	}
	if err := VerifyKeyOwner(key, userID); err != nil {
		return "", err
	}
	return key, nil
}

// UserPrefixes returns all of a user's owned prefixes in R2 in a fixed order.
// Account deletion clears these prefixes in bulk, so the id goes through the
// same segment rule as UserKey and cannot contain a dot (it is an identifier,
// not a file name). No prefix is produced for an empty or invalid id: a prefix
// like "vocals//" would cover other users' objects.
func UserPrefixes(userID string) ([]string, error) {
	if !segmentPattern.MatchString(userID) || strings.Contains(userID, ".") {
		return nil, ErrInvalidSegment
	}
	kinds := make([]string, 0, len(ownedKinds))
	for kind := range ownedKinds {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	prefixes := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		prefixes = append(prefixes, kind+"/"+userID+"/")
	}
	return prefixes, nil
}
