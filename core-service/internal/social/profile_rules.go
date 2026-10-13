package social

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// usernamePattern is spec §3: lower-case letters, digits, underscore, dot;
// 3-20 characters. A leading or trailing dot is rejected separately.
var usernamePattern = regexp.MustCompile(`^[a-z0-9_.]{3,20}$`)

const (
	maxUsernameLen    = 20
	maxDisplayNameLen = 40
	maxBioLen         = 160
	avatarPaletteSize = 8
)

// ValidUsername reports whether the name follows the contract rule.
func ValidUsername(username string) bool {
	return usernamePattern.MatchString(username) &&
		!strings.HasPrefix(username, ".") && !strings.HasSuffix(username, ".")
}

// AvatarColor derives a fixed palette index from 0 to 7 from the user ID:
// fnv32a(kullanici_id) % 8. It is not stored; the same ID always gives the
// same color.
func AvatarColor(userID string) int {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(userID))
	return int(hasher.Sum32() % avatarPaletteSize)
}

// usernameCandidates produces the suggestions to try, in order, for a taken
// name: name2, name3, ... name99. If the result would exceed 20 characters,
// the base is truncated.
func usernameCandidates(username string) []string {
	candidates := make([]string, 0, 98)
	for n := 2; n <= 99; n++ {
		suffix := strconv.Itoa(n)
		base := username
		if len(base)+len(suffix) > maxUsernameLen {
			base = base[:maxUsernameLen-len(suffix)]
		}
		candidate := base + suffix
		if ValidUsername(candidate) {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

// normalizeProfileInput trims the display name and bio and applies the
// rules. The username is NOT trimmed or lower-cased: mobile applies the same
// pattern, and a silent fix would store a different name than the one shown
// to the user.
func normalizeProfileInput(input ProfileInput) (ProfileInput, error) {
	if !ValidUsername(input.Username) {
		return input, ErrInvalidUsername
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Bio = strings.TrimSpace(input.Bio)
	if containsNUL(input.DisplayName, input.Bio) {
		return input, validationError("gorunenAd ve bio NUL karakteri iceremez")
	}
	if n := utf8.RuneCountInString(input.DisplayName); n < 1 || n > maxDisplayNameLen {
		return input, validationError("gorunenAd 1-40 karakter olmali")
	}
	if utf8.RuneCountInString(input.Bio) > maxBioLen {
		return input, validationError("bio en cok 160 karakter olabilir")
	}
	return input, nil
}

// parseID checks that a user or publication ID is a uuid and returns its
// canonical form.
func parseID(raw string) (string, bool) {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}
