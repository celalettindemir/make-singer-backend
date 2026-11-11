"""Mastering zinciri testleri: sentetik ses, ag ve ffmpeg yok."""

import re
import time

import numpy as np
from pydub import AudioSegment

from services.master import PROFILES, MasterService, _to_float

ORNEK_HIZI = 48000


def sinus(frekans, saniye=1.0, genlik=1.0, kanal=2):
    """Verilen genlikte (0-1) 16 bit sinus; genlik 1.0 tam olcek demek."""
    t = np.arange(int(ORNEK_HIZI * saniye)) / ORNEK_HIZI
    dalga = genlik * np.sin(2 * np.pi * frekans * t)
    tam = np.round(dalga * 32767).astype(np.int16)
    karisik = np.repeat(tam[:, None], kanal, axis=1).reshape(-1)
    return AudioSegment(
        karisik.tobytes(), frame_rate=ORNEK_HIZI, sample_width=2, channels=kanal
    )


def ornekler(ses):
    dizi = np.array(ses.get_array_of_samples(), dtype=np.float64)
    return dizi / float(2 ** (8 * ses.sample_width - 1))


def frekans_genligi(ses, frekans):
    """Sol kanalda tek bir frekansin genligi (DFT izdusumu)."""
    sol = ornekler(ses)[:: ses.channels]
    t = np.arange(len(sol)) / ses.frame_rate
    return 2 * abs(np.mean(sol * np.exp(-2j * np.pi * frekans * t)))


def test_tam_olcek_stemler_toplaninca_kirpilmaz():
    """Iki tam olcek stem ust uste binince tepe kirpilmamali, sekil korunmali."""
    a = sinus(440, genlik=0.9)
    b = sinus(440, genlik=0.9)

    karisim = MasterService()._mix_stems([a, b], [{}, {}])

    s = ornekler(karisim)
    kirpik = np.sum(np.abs(s) >= 0.999)
    assert kirpik == 0
    # Kirpilmis sinus kare dalgaya doner; temiz toplam 3. harmonik tasimaz.
    assert frekans_genligi(karisim, 1320) < 0.01 * frekans_genligi(karisim, 440)


def test_vokal_eklenince_kirpilmaz():
    muzik = sinus(220, genlik=0.9)
    vokal = sinus(220, genlik=0.9, kanal=1)

    toplam = MasterService._sum([muzik, vokal])

    assert np.sum(np.abs(ornekler(toplam)) >= 0.999) == 0
    assert toplam.channels == 2
    assert len(toplam) == len(muzik)


def test_toplam_kisa_parcayi_uzuna_tamamlar():
    uzun = sinus(220, saniye=1.0, genlik=0.2)
    kisa = sinus(220, saniye=0.5, genlik=0.2)

    assert len(MasterService._sum([uzun, kisa])) == len(uzun)


def test_warm_frekans_dengesini_korur():
    """Eski EQ sesi 200 Hz alt geciren ve 8 kHz ust geciren suzgeclerden
    art arda geciriyordu; normalize bunu tek tonda gizler, karisimda gizleyemez."""
    giris = sinus(60, genlik=0.3).overlay(sinus(1000, genlik=0.3))

    cikis = MasterService()._apply_mastering(giris, PROFILES["warm"])

    oran = frekans_genligi(cikis, 1000) / frekans_genligi(cikis, 60)
    # warm basi +2 dB, ortayi duz birakir: oran 1'in biraz altinda kalmali.
    assert 0.5 < oran < 1.0


def test_warm_basi_ortaya_gore_yukseltir():
    bas = sinus(60, genlik=0.3)
    orta = sinus(1000, genlik=0.3)
    servis = MasterService()
    eq = {"eq": PROFILES["warm"]["eq"]}

    bas_kazanc = frekans_genligi(servis._apply_eq(bas, eq), 60) / 0.3
    orta_kazanc = frekans_genligi(servis._apply_eq(orta, eq), 1000) / 0.3

    assert bas_kazanc > orta_kazanc * 1.15


def test_loud_tizi_ortaya_gore_yukseltir():
    tiz = sinus(12000, genlik=0.3)
    orta = sinus(1000, genlik=0.3)
    servis = MasterService()
    eq = {"eq": PROFILES["loud"]["eq"]}

    tiz_kazanc = frekans_genligi(servis._apply_eq(tiz, eq), 12000) / 0.3
    orta_kazanc = frekans_genligi(servis._apply_eq(orta, eq), 1000) / 0.3

    # Tek kutuplu shelf 12 kHz'de nominal +2 dB'nin ~+1 dB'ini verir.
    assert tiz_kazanc > orta_kazanc * 1.1


def test_duz_eq_sesi_degistirmez():
    giris = sinus(1000, genlik=0.3)

    cikis = MasterService()._apply_eq(giris, {"eq": PROFILES["clean"]["eq"]})

    assert np.allclose(ornekler(cikis), ornekler(giris), atol=1e-4)


def _yerlestir(vokal, **alanlar):
    return MasterService._place_take(vokal, alanlar)


def test_vokal_start_ms_kadar_sessizlikle_baslar():
    vokal = sinus(440, saniye=1.0, genlik=0.5)

    yerlesik = _yerlestir(vokal, start_ms=1000)

    assert len(yerlesik) == 2000
    s = ornekler(yerlesik)
    assert np.max(np.abs(s[: ORNEK_HIZI * yerlesik.channels])) == 0
    assert np.max(np.abs(s[ORNEK_HIZI * yerlesik.channels :])) > 0.4
    assert yerlesik.channels == vokal.channels
    assert yerlesik.frame_rate == vokal.frame_rate


def test_vokal_end_ms_ile_kesilir_ve_sonu_solar():
    vokal = sinus(440, saniye=2.0, genlik=0.5)

    yerlesik = _yerlestir(vokal, start_ms=500, end_ms=1500)

    assert len(yerlesik) == 1500  # 500 sessizlik + 1000 kesilmis vokal
    s = ornekler(yerlesik)[:: yerlesik.channels]
    assert np.max(np.abs(s[-10:])) < 0.05  # fade-out kesimde sifira iner
    assert np.max(np.abs(s[-ORNEK_HIZI // 10 : -ORNEK_HIZI // 20])) > 0.4


def test_vokal_end_ms_vokalden_uzunsa_dokunmaz():
    vokal = sinus(440, saniye=1.0, genlik=0.5)

    yerlesik = _yerlestir(vokal, end_ms=5000)

    assert len(yerlesik) == 1000
    assert ornekler(yerlesik).tolist() == ornekler(vokal).tolist()


def test_alan_yoksa_eski_davranis():
    vokal = sinus(440, saniye=1.0, genlik=0.5)

    yerlesik = _yerlestir(vokal, key="x", volume=1.0, start_ms=None, end_ms=None)

    assert yerlesik.raw_data == vokal.raw_data


def test_farkli_bolumlerdeki_vokaller_cakismaz():
    a = sinus(440, saniye=1.0, genlik=0.5)
    b = sinus(660, saniye=1.0, genlik=0.5)

    toplam = MasterService._sum(
        [_yerlestir(a, start_ms=0, end_ms=1000), _yerlestir(b, start_ms=1000)]
    )

    assert len(toplam) == 2000
    ilk = toplam[:1000]
    ikinci = toplam[1000:]
    assert frekans_genligi(ilk, 440) > 0.3
    assert frekans_genligi(ilk, 660) < 0.01
    assert frekans_genligi(ikinci, 660) > 0.3
    assert frekans_genligi(ikinci, 440) < 0.01


def test_pydantic_end_start_dogrulamasi():
    import pytest
    from pydantic import ValidationError

    from main import VocalTakeInput

    assert VocalTakeInput(key="k", start_ms=0, end_ms=10).end_ms == 10
    assert VocalTakeInput(key="k").start_ms is None
    with pytest.raises(ValidationError):
        VocalTakeInput(key="k", start_ms=100, end_ms=100)
    with pytest.raises(ValidationError):
        VocalTakeInput(key="k", start_ms=-1)


class _SahteDepo:
    """download_key_to_file yerine hazir bir sesi diske yazar."""

    def __init__(self, ses):
        self.ses = ses

    def download_key_to_file(self, key, yol):
        self.ses.export(yol, format="wav")


def _tepe(ses):
    return np.max(np.abs(ornekler(ses)))


def test_take_gain_applied(tmp_path):
    import pytest

    vokal = sinus(440, saniye=1.0, genlik=0.5)
    servis = MasterService()
    servis.storage = _SahteDepo(vokal)

    sifir = servis._load_vocals(str(tmp_path), [{"key": "k"}])
    kisik = servis._load_vocals(str(tmp_path), [{"key": "k", "gain_db": -6.0}])

    fark_db = 20 * np.log10(_tepe(kisik) / _tepe(sifir))
    assert fark_db == pytest.approx(-6.0, abs=0.5)


def test_pydantic_gain_db_sinirlari():
    import pytest
    from pydantic import ValidationError

    from main import VocalTakeInput

    assert VocalTakeInput(key="k").gain_db is None
    assert VocalTakeInput(key="k", gain_db=-6).gain_db == -6
    with pytest.raises(ValidationError):
        VocalTakeInput(key="k", gain_db=13)
    with pytest.raises(ValidationError):
        VocalTakeInput(key="k", gain_db=-61)


def test_master_olay_dongusunu_kilitlemez():
    # Master suresince /health cevap vermezse kubelet servisi oldurur ve
    # hemen ardindan gelen /encode istekleri "connection refused" alir.
    import asyncio

    servis = MasterService()
    servis.storage = object()

    def agir_zincir(*_):
        time.sleep(1.0)  # CPU ve R2 G/C gibi engelleyici
        return {"output_url": "https://depo/m.wav"}

    servis._process = agir_zincir

    async def senaryo():
        tikler = 0
        bitti = False

        async def saat():
            nonlocal tikler
            while not bitti:
                await asyncio.sleep(0.02)
                tikler += 1

        gorev = asyncio.create_task(saat())
        sonuc = await servis.process(
            stem_keys=["a"],
            mix_settings=[],
            profile="clean",
            vocal_takes=[],
            output_key="m.wav",
        )
        bitti = True
        await gorev
        return sonuc, tikler

    sonuc, tikler = asyncio.run(senaryo())
    assert sonuc["output_url"] == "https://depo/m.wav"
    # 1 s engelleyici is boyunca saat donmeye devam etmeli.
    assert tikler >= 20


# --- C vocal chain in the final master -------------------------------------

import logging
import shutil

import pytest

from services import master as master_modulu
from services import vocal_chain


class SahteDepo:
    """Anahtarlari tmp_path altindaki WAV dosyalarina esler."""

    def __init__(self, tmp_path):
        self.kok = tmp_path / "depo"
        self.kok.mkdir()
        self.yuklenen = {}

    def ekle(self, anahtar, ses):
        yol = self.kok / anahtar.replace("/", "_")
        ses.export(str(yol), format="wav")
        self.__dict__.setdefault("harita", {})[anahtar] = yol

    def download_key_to_file(self, anahtar, yol):
        harita = self.__dict__.get("harita", {})
        if anahtar not in harita:
            raise FileNotFoundError(anahtar)
        shutil.copy(harita[anahtar], yol)

    def upload_file(self, anahtar, yol, content_type):
        kopya = self.kok / ("yuklenen_" + anahtar.replace("/", "_"))
        shutil.copy(yol, kopya)
        self.yuklenen[anahtar] = str(kopya)
        return yol


def dizi_ses(x, hiz, kanal=1):
    """float (N,) -> 16 bit AudioSegment."""
    tam = np.round(np.clip(x, -1, 1) * 32767).astype(np.int16)
    karisik = np.repeat(tam[:, None], kanal, axis=1).reshape(-1)
    return AudioSegment(
        karisik.tobytes(), frame_rate=hiz, sample_width=2, channels=kanal
    )


def pembe_gurultu(saniye, hiz=44100, tohum=1):
    rng = np.random.default_rng(tohum)
    n = int(saniye * hiz)
    spec = np.fft.rfft(rng.standard_normal(n))
    f = np.maximum(np.arange(len(spec)), 1)
    x = np.fft.irfft(spec / np.sqrt(f), n)
    return x / np.max(np.abs(x))


def sarki_stem(saniye=20, hiz=44100):
    t = np.arange(int(saniye * hiz)) / hiz
    x = 0.25 * pembe_gurultu(saniye, hiz) + 0.25 * np.sin(2 * np.pi * 110 * t)
    return dizi_ses(x, hiz, kanal=2)


def sarki_gibi_vokal(saniye, hiz=44100, tohum=3):
    """Duzensiz araliklarla gelen harmonikli notalar: belirgin vurus zarfi."""
    rng = np.random.default_rng(tohum)
    n = int(saniye * hiz)
    x = np.zeros(n)
    t0 = 0.2
    while t0 < saniye - 0.5:
        uzun = rng.uniform(0.15, 0.5)
        f0 = 220.0
        i0, i1 = int(t0 * hiz), int((t0 + uzun) * hiz)
        t = np.arange(i1 - i0) / hiz
        nota = sum(np.sin(2 * np.pi * f0 * k * t) / k for k in range(1, 8))
        zarf = np.minimum(1, t / 0.01) * np.exp(-3 * t)
        x[i0:i1] += 0.3 * nota * zarf
        t0 += uzun + rng.uniform(0.1, 0.5)
    return x


def servis_kur(tmp_path, stem=None, **eklenecek):
    depo = SahteDepo(tmp_path)
    depo.ekle("stems/a.wav", stem if stem is not None else sarki_stem())
    for anahtar, ses in eklenecek.items():
        depo.ekle(anahtar.replace("__", "/"), ses)
    return MasterService(depo), depo


def calistir(servis, takes=(), profil="clean", guide_key=None, sure_stem=None):
    return servis._process(
        ["stems/a.wav"], [{}], profil, list(takes), "out/master.wav", guide_key
    )


def oku(depo):
    return AudioSegment.from_file(depo.yuklenen["out/master.wav"])


@pytest.mark.parametrize("profil", ["clean", "warm", "loud"])
def test_her_profilde_lufs_hedefte_tepe_esikte(tmp_path, profil):
    servis, depo = servis_kur(tmp_path)

    sonuc = calistir(servis, profil=profil)

    ayar = PROFILES[profil]
    assert sonuc["lufs"] == pytest.approx(ayar["target_lufs"], abs=0.5)
    cikti = oku(depo)
    assert cikti.frame_rate == 48000
    tepe_db = 20 * np.log10(np.max(np.abs(ornekler(cikti))))
    assert tepe_db <= ayar["limiter_threshold"] + 0.01
    assert sonuc["peak_db"] <= ayar["limiter_threshold"] + 0.01


def test_kullanici_take_yoksa_zincir_calismaz(tmp_path, monkeypatch):
    def boom(*_a, **_k):
        raise AssertionError("zincir calismamali")

    monkeypatch.setattr(vocal_chain, "process_vocal", boom)
    servis, _ = servis_kur(tmp_path, guides__app=dizi_ses(sarki_gibi_vokal(5), 44100))

    sonuc = calistir(servis, [{"key": "guides/app"}])

    assert sonuc["duration"] > 0


def test_ai_take_zincirden_gecmez(tmp_path, monkeypatch):
    cagrilar = []
    gercek = vocal_chain.process_vocal

    def izle(voc, rate, ref):
        cagrilar.append(np.asarray(voc).copy())
        return gercek(voc, rate, ref)

    monkeypatch.setattr(vocal_chain, "process_vocal", izle)
    uygulama = np.zeros(44100 * 6)
    uygulama[44100 * 4 :] = 0.5 * np.sin(2 * np.pi * 330 * np.arange(44100 * 2) / 44100)
    servis, _ = servis_kur(
        tmp_path,
        guides__app=dizi_ses(uygulama, 44100),
        vocals__kul=dizi_ses(sarki_gibi_vokal(4), 44100),
    )

    calistir(servis, [{"key": "guides/app"}, {"key": "vocals/kul"}])

    assert len(cagrilar) == 1
    # Uygulama vokali (330 Hz, 4 s sonrasi) zincire giren izde olmamali.
    iz = cagrilar[0]
    n4 = 44100 * 4
    assert len(iz) > n4 + 4410
    assert np.max(np.abs(iz[n4 + 4410 :])) < 1e-6


def test_rehber_yoksa_yerlesik_egri_ve_kayma_sifir(tmp_path, caplog):
    servis, _ = servis_kur(tmp_path, vocals__kul=dizi_ses(sarki_gibi_vokal(4), 44100))

    with caplog.at_level(logging.INFO, logger="uvicorn.error"):
        calistir(servis, [{"key": "vocals/kul"}])

    metin = caplog.text
    assert "vocal chain: ref=builtin-" in metin
    assert "shift_ms=0" in metin
    assert "vocal chain: vocal_lufs=" in metin


def test_rehber_indirilemezse_yerlesik_egri(tmp_path, caplog):
    servis, _ = servis_kur(tmp_path, vocals__kul=dizi_ses(sarki_gibi_vokal(4), 44100))

    with caplog.at_level(logging.INFO, logger="uvicorn.error"):
        sonuc = calistir(servis, [{"key": "vocals/kul"}], guide_key="guides/yok")

    assert sonuc["duration"] > 0
    assert "ref=builtin-" in caplog.text
    assert "guide guides/yok unusable" in caplog.text


def test_rehberle_kayma_uygulanir(tmp_path, caplog, monkeypatch):
    rehber = sarki_gibi_vokal(12)
    baslangic_ms = 2000
    parca = rehber[88200 : 88200 + 44100 * 5]
    gecikmis = np.concatenate([np.zeros(int(0.340 * 44100)), parca])
    ltas = vocal_chain.ltas_db(rehber, 44100)
    monkeypatch.setattr(master_modulu, "choose_reference", lambda *_a: ("guide", ltas))
    servis, _ = servis_kur(
        tmp_path,
        guides__rehber=dizi_ses(rehber, 44100),
        vocals__kul=dizi_ses(gecikmis, 44100),
    )

    with caplog.at_level(logging.INFO, logger="uvicorn.error"):
        calistir(
            servis,
            [{"key": "vocals/kul", "start_ms": baslangic_ms}],
            guide_key="guides/rehber",
        )

    bulunan = re.search(r"shift_ms=(-?\d+)", caplog.text)
    assert bulunan, caplog.text
    assert int(bulunan.group(1)) == pytest.approx(-340, abs=20)


def test_take_sarki_sonunu_asarsa_kirpilmaz(tmp_path):
    servis, _ = servis_kur(
        tmp_path,
        stem=sarki_stem(10),
        vocals__kul=dizi_ses(sarki_gibi_vokal(3), 44100),
    )

    sonuc = calistir(servis, [{"key": "vocals/kul", "start_ms": 9000}])

    assert sonuc["duration"] == pytest.approx(12.0, abs=0.05)


def test_farkli_ornekleme_hizlari_ayni_zaman_cizelgesi(tmp_path):
    sessiz = dizi_ses(np.zeros(22050 * 4), 22050, kanal=2)
    nota = 0.5 * np.sin(2 * np.pi * 440 * np.arange(48000 * 2) / 48000)
    servis, depo = servis_kur(tmp_path, stem=sessiz, vocals__kul=dizi_ses(nota, 48000))

    calistir(servis, [{"key": "vocals/kul", "start_ms": 1000}])

    ses = ornekler(oku(depo))[::2]
    esik = 0.05 * np.max(np.abs(ses))
    ilk = np.argmax(np.abs(ses) > esik) / 48000
    assert ilk == pytest.approx(1.0, abs=0.01)


@pytest.mark.parametrize("esik,oran", [(-20, 2.0), (-15, 4.0)])
def test_kompresor_pydub_ile_ayni_sonucu_verir(esik, oran):
    """Bellek dostu kompresor, pydub'un kare kare dongusuyle ayni sesi uretir."""
    from pydub.effects import compress_dynamic_range as pydub_kompresor

    from services.master import compress_dynamic_range

    zarf = np.concatenate(
        [np.full(9000, 0.05), np.full(12000, 0.8), np.full(9000, 0.1)]
    )
    t = np.arange(len(zarf)) / ORNEK_HIZI
    dalga = zarf * np.sin(2 * np.pi * 300 * t)
    ses = AudioSegment(
        np.round(np.repeat(dalga, 2) * 32767).astype("<i2").tobytes(),
        frame_rate=ORNEK_HIZI,
        sample_width=2,
        channels=2,
    )

    beklenen = np.array(
        pydub_kompresor(ses, esik, oran).get_array_of_samples(), dtype=np.int64
    )
    sonuc = np.array(
        compress_dynamic_range(ses, esik, oran).get_array_of_samples(), dtype=np.int64
    )

    assert len(sonuc) == len(beklenen)
    assert np.abs(sonuc - beklenen).max() <= 2


def _tam_olcek_stem(saniye=8, hiz=44100):
    t = np.arange(int(saniye * hiz)) / hiz
    x = 0.5 * pembe_gurultu(saniye, hiz) + 0.5 * np.sin(2 * np.pi * 110 * t)
    return dizi_ses(x / np.max(np.abs(x)), hiz, kanal=2)


def test_tam_olcek_enstrumantal_ve_take_zincirde_kirpilmaz(tmp_path):
    servis, _ = servis_kur(tmp_path, vocals__kul=dizi_ses(sarki_gibi_vokal(6), 44100))
    stem = _tam_olcek_stem()
    take = {"key": "vocals/kul", "start_ms": 500}

    zincirli, _ = servis._apply_vocal_chain(str(tmp_path), [stem], [take], None)

    x = _to_float(zincirli)
    tam = np.sum(np.abs(x) >= 0.999)
    assert tam == 0
    assert np.max(np.abs(x)) <= master_modulu.HEADROOM_PEAK + 1e-3


def _format(yol):
    import wave

    with wave.open(yol, "rb") as w:
        return w.getsampwidth(), w.getframerate(), w.getnchannels()


def test_cikis_16_bit_48_khz_stereo_takesiz(tmp_path):
    servis, depo = servis_kur(tmp_path)
    servis._process(["stems/a.wav"], [{}], "warm", [], "out/master.wav", None)
    assert _format(depo.yuklenen["out/master.wav"]) == (2, 48000, 2)


def test_cikis_16_bit_48_khz_stereo_take_ile(tmp_path):
    servis, depo = servis_kur(
        tmp_path, vocals__kul=dizi_ses(sarki_gibi_vokal(4), 44100)
    )
    calistir(servis, [{"key": "vocals/kul"}], profil="loud")
    assert _format(depo.yuklenen["out/master.wav"]) == (2, 48000, 2)


def test_48_khz_stem_15_khz_ton_kullanici_take_yolunda_korunur(tmp_path):
    hiz = 48000
    t = np.arange(hiz * 6) / hiz
    ton = 0.03 * np.sin(2 * np.pi * 15000 * t)
    stem = dizi_ses(ton, hiz, kanal=2)
    # Take is 1 s long at 4.5 s, so 1 s to 4 s is instrumental only.
    servis, _ = servis_kur(
        tmp_path, stem=stem, vocals__kul=dizi_ses(sarki_gibi_vokal(1), 44100)
    )
    take = {"key": "vocals/kul", "start_ms": 4500}

    sonuc, _ = servis._apply_vocal_chain(str(tmp_path), [stem], [take], None)

    assert sonuc.frame_rate == 44100
    x = _to_float(sonuc)[:, 0][44100 : 44100 * 4]
    spek = np.abs(np.fft.rfft(x * np.hanning(len(x))))
    f = np.fft.rfftfreq(len(x), 1 / 44100)
    tepe = spek[np.abs(f - 15000) < 20].max()
    beklenen = 0.03 * np.sum(np.hanning(len(x))) / 2
    assert 20 * np.log10(tepe / beklenen) == pytest.approx(0, abs=0.5)
    sahte = spek[np.abs(f - 15000) > 300].max()
    assert 20 * np.log10(sahte / tepe) < -60


def test_kompresor_pydub_ile_ayni_sonucu_verir_sesli_baslangic_32_bit():
    from pydub.effects import compress_dynamic_range as pydub_kompresor

    from services.master import compress_dynamic_range

    n = 12000
    t = np.arange(n) / ORNEK_HIZI
    zarf = np.where(t < 0.12, 0.9, 0.05)  # loud from the very first frame
    dalga = zarf * np.sin(2 * np.pi * 300 * t)
    ses = AudioSegment(
        np.round(np.repeat(dalga, 2) * (2**31 - 1)).astype("<i4").tobytes(),
        frame_rate=ORNEK_HIZI,
        sample_width=4,
        channels=2,
    )

    beklenen = np.array(
        pydub_kompresor(ses, -20, 4.0).get_array_of_samples(), dtype=np.int64
    )
    sonuc = np.array(
        compress_dynamic_range(ses, -20, 4.0).get_array_of_samples(), dtype=np.int64
    )

    assert len(sonuc) == len(beklenen)
    assert np.abs(sonuc - beklenen).max() <= 64  # int32 LSB, incl. the first 10 ms


def test_ayni_anda_iki_master_birbirini_bekler():
    import asyncio
    import threading

    servis = MasterService()
    servis.storage = object()
    ayni_anda = 0
    en_cok = 0
    kilit = threading.Lock()

    def zincir(*_):
        nonlocal ayni_anda, en_cok
        with kilit:
            ayni_anda += 1
            en_cok = max(en_cok, ayni_anda)
        time.sleep(0.2)
        with kilit:
            ayni_anda -= 1
        return {"output_url": "u"}

    servis._process = zincir

    async def senaryo():
        istek = {
            "stem_keys": ["a"],
            "mix_settings": [],
            "profile": "clean",
            "vocal_takes": [],
            "output_key": "m.wav",
        }
        await asyncio.gather(servis.process(**istek), servis.process(**istek))

    asyncio.run(senaryo())
    assert en_cok == 1
