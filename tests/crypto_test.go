package tests

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vex-language/vex-lang/compiler/cc"
)

// A test vector for vex:crypto: a line for testdata/crypto/vectors.vx and
// what it must print.
type cryptoVector struct {
	source string
	line   string

	// The result, or `*` when any result is acceptable.
	want string
}

// Returns the hex of [s], or `-` when it is empty, as vectors.vx reads
// and writes empty values.
func hx(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Returns [s] as hex.
func text(s string) string { return hx(hex.EncodeToString([]byte(s))) }

// Returns [n] copies of the byte [b], as hex.
func rep(b byte, n int) string { return strings.Repeat(fmt.Sprintf("%02x", b), n) }

// Returns the bytes from [from] up to and including [to], as hex.
func seq(from, to int) string {
	var sb strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&sb, "%02x", i)
	}
	return sb.String()
}

func v(source, want string, op string, args ...any) cryptoVector {
	fields := []string{op}
	for _, a := range args {
		switch a := a.(type) {
		case int:
			fields = append(fields, fmt.Sprintf("#%d", a))
		case string:
			fields = append(fields, hx(a))
		}
	}
	return cryptoVector{source, strings.Join(fields, " "), want}
}

const ladies = "Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it."

// Test vectors from the standards that define each primitive.
func standardVectors() []cryptoVector {
	million := strings.Repeat("a", 1000000)
	long256 := "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"
	long512 := "abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu"
	bigKey := rep(0xaa, 131)
	return []cryptoVector{
		// FIPS 180-4 examples.
		v("SHA-256 abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "sha256", text("abc")),
		v("SHA-256 empty", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "sha256", ""),
		v("SHA-256 448 bits", "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1", "sha256", text(long256)),
		v("SHA-256 million", "cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0", "sha256", text(million)),
		v("SHA-512 abc", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f", "sha512", text("abc")),
		v("SHA-512 empty", "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e", "sha512", ""),
		v("SHA-512 896 bits", "8e959b75dae313da8cf4f72814fc143f8f7779c6eb9f7fa17299aeadb6889018501d289e4900f7e4331b99dec4b5433ac7d329eeb6dd26545e96e55b874be909", "sha512", text(long512)),
		v("SHA-512 million", "e718483d0ce769644e2e42c7bc15b4638e1f98b13b2044285632a803afa973ebde0ff244877ea60a4cb0432ce577c31beb009c5c2c49aa2e4eadb217ad8cc09b", "sha512", text(million)),
		// RFC 1321 and RFC 3174.
		v("MD5 abc", "900150983cd24fb0d6963f7d28e17f72", "md5", text("abc")),
		v("SHA-1 abc", "a9993e364706816aba3e25717850c26c9cd0d89d", "sha1", text("abc")),
		// RFC 7693, appendix A.
		v("BLAKE2b-512 abc", "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923", "blake2b", 64, "", text("abc")),
		// RFC 4231, test cases 1, 2, 3, 4, 6 and 7.
		v("HMAC-SHA-256 1", "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7", "hmac256", rep(0x0b, 20), text("Hi There")),
		v("HMAC-SHA-256 2", "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843", "hmac256", text("Jefe"), text("what do ya want for nothing?")),
		v("HMAC-SHA-256 3", "773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe", "hmac256", rep(0xaa, 20), rep(0xdd, 50)),
		v("HMAC-SHA-256 4", "82558a389a443c0ea4cc819899f2083a85f0faa3e578f8077a2e3ff46729665b", "hmac256", seq(1, 25), rep(0xcd, 50)),
		v("HMAC-SHA-256 6", "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54", "hmac256", bigKey, text("Test Using Larger Than Block-Size Key - Hash Key First")),
		v("HMAC-SHA-256 7", "9b09ffa71b942fcb27635fbcd5b0e944bfdc63644f0713938a7f51535c3a35e2", "hmac256", bigKey, text("This is a test using a larger than block-size key and a larger than block-size data. The key needs to be hashed before being used by the HMAC algorithm.")),
		v("HMAC-SHA-512 1", "87aa7cdea5ef619d4ff0b4241a1d6cb02379f4e2ce4ec2787ad0b30545e17cdedaa833b7d6b8a702038b274eaea3f4e4be9d914eeb61f1702e696c203a126854", "hmac512", rep(0x0b, 20), text("Hi There")),
		v("HMAC-SHA-512 2", "164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea2505549758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737", "hmac512", text("Jefe"), text("what do ya want for nothing?")),
		v("HMAC-SHA-512 3", "fa73b0089d56a284efb0f0756c890be9b1b5dbdd8ee81a3655f83e33b2279d39bf3e848279a722c806b485a47e67c807b946a337bee8942674278859e13292fb", "hmac512", rep(0xaa, 20), rep(0xdd, 50)),
		v("HMAC-SHA-512 4", "b0ba465637458c6990e5a8c5f61d4af7e576d97ff94b872de76f8050361ee3dba91ca5c11aa25eb4d679275cc5788063a5f19741120c4f2de2adebeb10a298dd", "hmac512", seq(1, 25), rep(0xcd, 50)),
		v("HMAC-SHA-512 6", "80b24263c7c1a3ebb71493c1dd7be8b49b46d1f41b4aeec1121b013783f8f3526b56d037e05f2598bd0fd2215d6a1e5295e64f73f63f0aec8b915a985d786598", "hmac512", bigKey, text("Test Using Larger Than Block-Size Key - Hash Key First")),
		v("HMAC-SHA-512 7", "e37b6a775dc87dbaa4dfa9f96e5e3ffddebd71f8867289865df5a32d20cdc944b6022cac3c4982b10d5eeb55c3e4de15134676fb6de0446065c97440fa8c6a58", "hmac512", bigKey, text("This is a test using a larger than block-size key and a larger than block-size data. The key needs to be hashed before being used by the HMAC algorithm.")),
		// RFC 5869, test cases 1 to 3.
		v("HKDF-SHA-256 1", "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865", "hkdf256", rep(0x0b, 22), seq(0, 12), seq(0xf0, 0xf9), 42),
		v("HKDF-SHA-256 2", "b11e398dc80327a1c8e7f78c596a49344f012eda2d4efad8a050cc4c19afa97c59045a99cac7827271cb41c65e590e09da3275600c2f09b8367793a9aca3db71cc30c58179ec3e87c14c01d5c1f3434f1d87", "hkdf256", seq(0, 0x4f), seq(0x60, 0xaf), seq(0xb0, 0xff), 82),
		v("HKDF-SHA-256 3", "8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8", "hkdf256", rep(0x0b, 22), "", "", 42),
		v("HKDF-SHA-256 too long", "fail", "hkdf256", rep(0x0b, 22), "", "", 255*32+1),
		// RFC 7914, section 11.
		v("PBKDF2-SHA-256 1", "55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc49ca9cccf179b645991664b39d77ef317c71b845b1e30bd509112041d3a19783", "pbkdf2", text("passwd"), text("salt"), 1, 64),
		v("PBKDF2-SHA-256 2", "4ddcd8f60b98be21830cee5ef22701f9641a4418d04c0414aeff08876b34ab56a1d425a1225833549adb841b51c9b3176a272bdebba1d078478f62b397f33c8d", "pbkdf2", text("Password"), text("NaCl"), 80000, 64),
		// RFC 9106, section 5.3.
		v("Argon2id", "0d640df58d78766c08c037a34a8b53c9d01ef0452d75b65eb52520e96b01e659", "argon2id", rep(1, 32), rep(2, 16), rep(3, 8), rep(4, 12), 32, 32, 3, 4),
		// RFC 8439, section 2.8.2, and draft-irtf-cfrg-xchacha-03, A.3.1.
		v("ChaCha20-Poly1305 seal", "d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4def08e4b7a9de576d26586cec64b6116"+"1ae10b594f09e26a7e902ecbd0600691", "chacha-seal", seq(0x80, 0x9f), "070000004041424344454647", "50515253c0c1c2c3c4c5c6c7", text(ladies)),
		v("ChaCha20-Poly1305 open", text(ladies), "chacha-open", seq(0x80, 0x9f), "070000004041424344454647", "50515253c0c1c2c3c4c5c6c7", "d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4def08e4b7a9de576d26586cec64b6116"+"1ae10b594f09e26a7e902ecbd0600691"),
		v("ChaCha20-Poly1305 tampered", "fail", "chacha-open", seq(0x80, 0x9f), "070000004041424344454647", "50515253c0c1c2c3c4c5c6c7", "d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4def08e4b7a9de576d26586cec64b6116"+"1ae10b594f09e26a7e902ecbd0600692"),
		v("XChaCha20-Poly1305 seal", "bd6d179d3e83d43b9576579493c0e939572a1700252bfaccbed2902c21396cbb731c7f1b0b4aa6440bf3a82f4eda7e39ae64c6708c54c216cb96b72e1213b4522f8c9ba40db5d945b11b69b982c1bb9e3f3fac2bc369488f76b2383565d3fff921f9664c97637da9768812f615c68b13b52e"+"c0875924c1c7987947deafd8780acf49", "chacha-seal", seq(0x80, 0x9f), seq(0x40, 0x57), "50515253c0c1c2c3c4c5c6c7", text(ladies)),
		// The GCM specification's test cases 13 to 16, with 256-bit keys.
		v("AES-256-GCM 13", "530f8afbc74536b9a963b4f1c4cb738b", "gcm-seal", rep(0, 32), rep(0, 12), "", ""),
		v("AES-256-GCM 14", "cea7403d4d606b6e074ec5d3baf39d18d0d1c8a799996bf0265b98b5d48ab919", "gcm-seal", rep(0, 32), rep(0, 12), "", rep(0, 16)),
		v("AES-256-GCM 15", "522dc1f099567d07f47f37a32a84427d643a8cdcbfe5c0c97598a2bd2555d1aa8cb08e48590dbb3da7b08b1056828838c5f61e6393ba7a0abcc9f662898015ad"+"b094dac5d93471bdec1a502270e3cc6c", "gcm-seal", "feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308", "cafebabefacedbaddecaf888", "", "d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b391aafd255"),
		v("AES-256-GCM 16", "522dc1f099567d07f47f37a32a84427d643a8cdcbfe5c0c97598a2bd2555d1aa8cb08e48590dbb3da7b08b1056828838c5f61e6393ba7a0abcc9f662"+"76fc6ece0f4e1768cddf8853bb2d551b", "gcm-seal", "feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308", "cafebabefacedbaddecaf888", "feedfacedeadbeeffeedfacedeadbeefabaddad2", "d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39"),
		v("AES-256-GCM 16 open", "d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39", "gcm-open", "feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308", "cafebabefacedbaddecaf888", "feedfacedeadbeeffeedfacedeadbeefabaddad2", "522dc1f099567d07f47f37a32a84427d643a8cdcbfe5c0c97598a2bd2555d1aa8cb08e48590dbb3da7b08b1056828838c5f61e6393ba7a0abcc9f662"+"76fc6ece0f4e1768cddf8853bb2d551b"),
		v("AES-256-GCM 16 tampered", "fail", "gcm-open", "feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308", "cafebabefacedbaddecaf888", "feedfacedeadbeeffeedfacedeadbeefabaddad3", "522dc1f099567d07f47f37a32a84427d643a8cdcbfe5c0c97598a2bd2555d1aa8cb08e48590dbb3da7b08b1056828838c5f61e6393ba7a0abcc9f662"+"76fc6ece0f4e1768cddf8853bb2d551b"),
		// RFC 8032, section 7.1, tests 1 to 3.
		v("Ed25519 1", "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b", "ed25519-sign", "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60", ""),
		v("Ed25519 2", "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c 92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00", "ed25519-sign", "4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb", "72"),
		v("Ed25519 3", "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025 6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac18ff9b538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a", "ed25519-sign", "c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7", "af82"),
		v("Ed25519 3 verify", "valid", "ed25519-verify", "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025", "af82", "6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac18ff9b538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a"),
		v("Ed25519 3 other message", "invalid", "ed25519-verify", "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025", "af83", "6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac18ff9b538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a"),
		// RFC 7748, sections 5.2 and 6.1.
		v("X25519 1", "c3da55379de9c6908e94ea4df28d084f32eccf03491c71f754b4075577a28552", "x25519", "a546e36bf0527c9d3b16154b82465edd62144c0ac1fc5a18506a2244ba449ac4", "e6db6867583030db3594c1a424b15f7c726624ec26b3353b10a903a6d0ab1c4c"),
		v("X25519 2", "95cbde9476e8907d7aade45cb4b873f88b595a68799fa152e6f8f7647aac7957", "x25519", "4b66e9d4d1b4673c5ad22691957d6af5c11b6421e0ea01d42ca4169e7918ba0d", "e5210f12786811d3f4b7959d0538ae2c31dbe7106fc03c3efc4cd549c715a493"),
		v("X25519 Alice's public key", "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a", "x25519-public", "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a"),
		v("X25519 Bob's public key", "de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f", "x25519-public", "5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb"),
		v("X25519 shared secret", "4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742", "x25519", "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a", "de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f"),
		v("X25519 zero public key", "fail", "x25519", "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a", rep(0, 32)),
		// RFC 6979, appendix A.2.5: P-256 with SHA-256, message "sample".
		v("P-256 RFC 6979", "valid", "p256-verify", "0460fed4ba255a9d31c961eb74c6356d68c049b8923b61fa6ce669622e60f29fb67903fe1008b8bc99a41ae9e95628bc64f2f1b20c2d7e9f5177a3c294d4462299", text("sample"), derSig("efd48b2aacb6a8fd1140dd9cd45e81d69d2c877b56aaf991c34d0ea84eaf3716", "f7cb1c942d657c41d436c7a1b6e29f65f3e900dbb9aff4064dc4ab2f843acda8")),
		v("P-256 RFC 6979 other message", "invalid", "p256-verify", "0460fed4ba255a9d31c961eb74c6356d68c049b8923b61fa6ce669622e60f29fb67903fe1008b8bc99a41ae9e95628bc64f2f1b20c2d7e9f5177a3c294d4462299", text("samplf"), derSig("efd48b2aacb6a8fd1140dd9cd45e81d69d2c877b56aaf991c34d0ea84eaf3716", "f7cb1c942d657c41d436c7a1b6e29f65f3e900dbb9aff4064dc4ab2f843acda8")),
	}
}

// Returns the DER encoding of the ECDSA signature (r, s), given in hex.
func derSig(r, s string) string {
	integer := func(h string) []byte {
		b, _ := hex.DecodeString(h)
		for len(b) > 1 && b[0] == 0 && b[1] < 0x80 {
			b = b[1:]
		}
		if b[0] >= 0x80 {
			b = append([]byte{0}, b...)
		}
		return append([]byte{2, byte(len(b))}, b...)
	}
	body := append(integer(r), integer(s)...)
	return hex.EncodeToString(append([]byte{0x30, byte(len(body))}, body...))
}

// A file of Wycheproof test vectors: groups of tests, each group sharing
// fields such as a public key.
type wycheproofFile struct {
	TestGroups []struct {
		IvSize    int `json:"ivSize"`
		KeySize   int `json:"keySize"`
		TagSize   int `json:"tagSize"`
		PublicKey struct {
			Pk           string `json:"pk"`
			Uncompressed string `json:"uncompressed"`
		} `json:"publicKey"`
		Tests []map[string]any `json:"tests"`
	} `json:"testGroups"`
}

// Returns the vectors of testdata/wycheproof/[name].json, made by [make]
// for each test of each group; [make] may return none for a test that
// doesn't apply.
func wycheproof(t *testing.T, name string, make func(g int, f *wycheproofFile, tc map[string]any) []cryptoVector) []cryptoVector {
	data, err := os.ReadFile(filepath.Join("testdata", "wycheproof", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var f wycheproofFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out []cryptoVector
	for g, group := range f.TestGroups {
		for _, tc := range group.Tests {
			for _, vec := range make(g, &f, tc) {
				vec.source = fmt.Sprintf("Wycheproof %s #%v %v", name, tc["tcId"], tc["comment"])
				out = append(out, vec)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: no vectors apply", name)
	}
	return out
}

func str(tc map[string]any, key string) string { s, _ := tc[key].(string); return s }

// Returns the vectors of the Wycheproof AEAD file [name] for [op]
// (chacha or gcm) whose key, nonce and tag have the sizes the module uses.
func aeadVectors(t *testing.T, name, op string, keyBits, ivBits int) []cryptoVector {
	return wycheproof(t, name, func(g int, f *wycheproofFile, tc map[string]any) []cryptoVector {
		group := f.TestGroups[g]
		if group.KeySize != keyBits || group.IvSize != ivBits || group.TagSize != 128 {
			return nil
		}
		key, iv, aad, msg := str(tc, "key"), str(tc, "iv"), str(tc, "aad"), str(tc, "msg")
		box := str(tc, "ct") + str(tc, "tag")
		switch str(tc, "result") {
		case "valid":
			return []cryptoVector{
				v("", box, op+"-seal", key, iv, aad, msg),
				v("", hx(msg), op+"-open", key, iv, aad, box),
			}
		case "invalid":
			return []cryptoVector{v("", "fail", op+"-open", key, iv, aad, box)}
		}
		return []cryptoVector{v("", "*", op+"-open", key, iv, aad, box)}
	})
}

// Returns the expected result of a Wycheproof signature test: `*` for one
// that implementations may accept or reject.
func verdict(tc map[string]any) string {
	switch str(tc, "result") {
	case "valid":
		return "valid"
	case "invalid":
		return "invalid"
	}
	return "*"
}

// Returns the vectors of the Wycheproof files the module is tested with.
func wycheproofVectors(t *testing.T) []cryptoVector {
	var out []cryptoVector
	out = append(out, aeadVectors(t, "aes_gcm_test", "gcm", 256, 96)...)
	out = append(out, aeadVectors(t, "chacha20_poly1305_test", "chacha", 256, 96)...)
	out = append(out, aeadVectors(t, "xchacha20_poly1305_test", "chacha", 256, 192)...)
	out = append(out, wycheproof(t, "ed25519_test", func(g int, f *wycheproofFile, tc map[string]any) []cryptoVector {
		return []cryptoVector{v("", verdict(tc), "ed25519-verify", f.TestGroups[g].PublicKey.Pk, str(tc, "msg"), str(tc, "sig"))}
	})...)
	out = append(out, wycheproof(t, "ecdsa_secp256r1_sha256_test", func(g int, f *wycheproofFile, tc map[string]any) []cryptoVector {
		return []cryptoVector{v("", verdict(tc), "p256-verify", f.TestGroups[g].PublicKey.Uncompressed, str(tc, "msg"), str(tc, "sig"))}
	})...)
	out = append(out, wycheproof(t, "x25519_test", func(g int, f *wycheproofFile, tc map[string]any) []cryptoVector {
		// The module refuses the all-zero secret of a small-order key.
		want := str(tc, "shared")
		if want == rep(0, 32) {
			want = "fail"
		}
		return []cryptoVector{v("", want, "x25519", str(tc, "private"), str(tc, "public"))}
	})...)
	for _, name := range []string{"hmac_sha256_test", "hmac_sha512_test"} {
		op, full := "hmac256", 256
		if name == "hmac_sha512_test" {
			op, full = "hmac512", 512
		}
		out = append(out, wycheproof(t, name, func(g int, f *wycheproofFile, tc map[string]any) []cryptoVector {
			// Only full-length tags; the module never truncates them.
			if f.TestGroups[g].TagSize != full || str(tc, "result") != "valid" {
				return nil
			}
			return []cryptoVector{v("", str(tc, "tag"), op, str(tc, "key"), str(tc, "msg"))}
		})...)
	}
	for _, name := range []string{"hkdf_sha256_test", "hkdf_sha512_test"} {
		op := strings.Replace(strings.TrimSuffix(name, "_test"), "_sha", "", 1)
		out = append(out, wycheproof(t, name, func(g int, f *wycheproofFile, tc map[string]any) []cryptoVector {
			want := str(tc, "okm")
			if str(tc, "result") != "valid" {
				want = "fail"
			}
			return []cryptoVector{v("", want, op, str(tc, "ikm"), str(tc, "salt"), str(tc, "info"), int(tc["size"].(float64)))}
		})...)
	}
	out = append(out, wycheproof(t, "pbkdf2_hmacsha256_test", func(g int, f *wycheproofFile, tc map[string]any) []cryptoVector {
		return []cryptoVector{v("", str(tc, "dk"), "pbkdf2", str(tc, "password"), str(tc, "salt"), int(tc["iterationCount"].(float64)), int(tc["dkLen"].(float64)))}
	})...)
	return out
}

// Runs the standard and Wycheproof test vectors through a Vex program
// that calls vex:crypto.
func TestCryptoVectors(t *testing.T) {
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
	vectors := append(standardVectors(), wycheproofVectors(t)...)
	src, opts, err := compileProgram(filepath.Join("testdata", "crypto", "vectors.vx"))
	if err != nil {
		t.Fatalf("compile errors:\n%v", err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "vectors")
	if err := cc.CompileWith(src, exe, opts); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, vec := range vectors {
		lines = append(lines, vec.line)
	}
	input := filepath.Join(dir, "vectors.txt")
	if err := os.WriteFile(input, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe, input).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(got) != len(vectors) {
		t.Fatalf("got %d results for %d vectors:\n%s", len(got), len(vectors), out)
	}
	t.Logf("%d vectors", len(vectors))
	for i, vec := range vectors {
		if vec.want != "*" && got[i] != vec.want {
			t.Errorf("%s: %s\n got  %s\n want %s", vec.source, vec.line[:min(len(vec.line), 120)], got[i], vec.want)
		}
	}
}
