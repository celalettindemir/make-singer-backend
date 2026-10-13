package social

import (
	"path"
	"regexp"

	"github.com/makeasinger/api/internal/client"
)

// extensionPattern is the same extension rule as the segment pattern.
var extensionPattern = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

// extension returns the source key's extension, or empty if it does not match the pattern.
func extension(source string) string {
	ext := path.Ext(source)
	if !extensionPattern.MatchString(ext) {
		return ""
	}
	return ext
}

// MasterDestKey is the key of the master copy: sosyal/{id}/master.{ext}.
func MasterDestKey(pubID, source string) (string, error) {
	return client.SocialKey(pubID, "master"+extension(source))
}

// StemDestKey is the key of a stem copy: sosyal/{id}/stems/{stemId}.{ext}.
func StemDestKey(pubID, stemID, source string) (string, error) {
	return client.SocialKey(pubID, "stems", stemID+extension(source))
}

// PreviewDestKey is the key of the 30 s preview: sosyal/{id}/onizleme.mp3.
func PreviewDestKey(pubID string) (string, error) {
	return client.SocialKey(pubID, "onizleme.mp3")
}
