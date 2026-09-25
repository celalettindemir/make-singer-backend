"""StorageService testleri. Ag erisimi yok: sahte S3 istemcisi enjekte edilir."""

import pytest

from services.storage import StorageService, bucket_for


class SahteS3:
    """boto3 s3 istemcisinin testte kullanilan uclari."""

    def __init__(self):
        self.yuklenenler = []
        self.indirilenler = []
        self.silinenler = []

    def upload_fileobj(self, data, bucket, key, ExtraArgs=None):
        self.yuklenenler.append((bucket, key, ExtraArgs))

    def download_file(self, bucket, key, path):
        self.indirilenler.append((bucket, key, path))
        with open(path, "wb") as f:
            f.write(b"ses")

    def delete_object(self, Bucket, Key):
        self.silinenler.append((Bucket, Key))


@pytest.fixture
def servis():
    return StorageService(
        account_id="hesap",
        access_key_id="anahtar",
        secret_access_key="gizli",
        public_bucket="makeasinger-public",
        private_bucket="makeasinger-private",
        public_url="https://makesinger-cdn.celalettindemir.dev",
        s3_client=SahteS3(),
    )


@pytest.mark.parametrize(
    "anahtar,beklenen",
    [
        ("exports/a.mp3", "makeasinger-public"),
        ("exports/a.zip", "makeasinger-public"),
        ("vocals/p/s/t.wav", "makeasinger-private"),
        ("masters/p/m.wav", "makeasinger-private"),
        ("stems/p/s.wav", "makeasinger-private"),
        ("bilinmeyen/x.bin", "makeasinger-private"),
        ("", "makeasinger-private"),
        ("exportsa.mp3", "makeasinger-private"),
    ],
)
def test_bucket_for(anahtar, beklenen):
    assert bucket_for(anahtar, "makeasinger-public", "makeasinger-private") == beklenen


def test_upload_dogru_bucket_secer(servis):
    servis.upload("exports/a.mp3", b"veri", "audio/mpeg")
    servis.upload("vocals/p/s/t.wav", b"veri", "audio/wav")

    bucketlar = [y[0] for y in servis.s3_client.yuklenenler]
    assert bucketlar == ["makeasinger-public", "makeasinger-private"]


def test_url_for_public_cdn_adresi(servis):
    assert servis.url_for("exports/a.mp3") == "https://makesinger-cdn.celalettindemir.dev/exports/a.mp3"


def test_url_for_private_cdn_adresi_vermez(servis):
    url = servis.url_for("vocals/p/s/t.wav")
    assert "makesinger-cdn" not in url
    assert "makeasinger-private" in url


def test_download_key_to_file_s3_kullanir(servis, tmp_path):
    hedef = tmp_path / "in.wav"
    servis.download_key_to_file("masters/p/m.wav", str(hedef))

    assert servis.s3_client.indirilenler == [("makeasinger-private", "masters/p/m.wav", str(hedef))]
    assert hedef.read_bytes() == b"ses"


def test_delete_dogru_bucket(servis):
    servis.delete("stems/p/s.wav")
    assert servis.s3_client.silinenler == [("makeasinger-private", "stems/p/s.wav")]
