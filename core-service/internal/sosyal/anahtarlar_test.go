package sosyal

import "testing"

func TestHedefAnahtarlari(t *testing.T) {
	id := "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	if got, err := MasterHedefi(id, "masters/u/p/m1.wav"); err != nil || got != "sosyal/"+id+"/master.wav" {
		t.Fatalf("master = %q, %v", got, err)
	}
	if got, err := MasterHedefi(id, "masters/u/p/m1"); err != nil || got != "sosyal/"+id+"/master" {
		t.Fatalf("uzantisiz master = %q, %v", got, err)
	}
	if got, err := StemHedefi(id, "drums", "stems/u/p/drums.mp3"); err != nil || got != "sosyal/"+id+"/stems/drums.mp3" {
		t.Fatalf("stem = %q, %v", got, err)
	}
	if got, err := OnizlemeHedefi(id); err != nil || got != "sosyal/"+id+"/onizleme.mp3" {
		t.Fatalf("onizleme = %q, %v", got, err)
	}
	if _, err := StemHedefi(id, "a.b", "stems/u/p/x.mp3"); err == nil {
		t.Fatal("noktali stem id reddedilmeli")
	}
	if _, err := StemHedefi(id, "../x", "stems/u/p/x.mp3"); err == nil {
		t.Fatal("tirmanan stem id reddedilmeli")
	}
	if got := uzanti("stems/u/p/x.WAV"); got != "" {
		t.Fatalf("buyuk harfli uzanti segment desenine uymaz, bos olmali: %q", got)
	}
}
