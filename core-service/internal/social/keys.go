package social

import (
	"path"
	"regexp"

	"github.com/makeasinger/api/internal/client"
)

// uzantiDeseni, segment desenindeki uzanti kuraliyla aynidir.
var uzantiDeseni = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

// uzanti, kaynak anahtarin uzantisini doner; desene uymuyorsa bos.
func uzanti(kaynak string) string {
	u := path.Ext(kaynak)
	if !uzantiDeseni.MatchString(u) {
		return ""
	}
	return u
}

// MasterHedefi, master kopyasinin anahtaridir: sosyal/{id}/master.{ext}.
func MasterHedefi(yayinID, kaynak string) (string, error) {
	return client.SocialKey(yayinID, "master"+uzanti(kaynak))
}

// StemHedefi, stem kopyasinin anahtaridir: sosyal/{id}/stems/{stemId}.{ext}.
func StemHedefi(yayinID, stemID, kaynak string) (string, error) {
	return client.SocialKey(yayinID, "stems", stemID+uzanti(kaynak))
}

// OnizlemeHedefi, 30 sn onizlemenin anahtaridir: sosyal/{id}/onizleme.mp3.
func OnizlemeHedefi(yayinID string) (string, error) {
	return client.SocialKey(yayinID, "onizleme.mp3")
}
