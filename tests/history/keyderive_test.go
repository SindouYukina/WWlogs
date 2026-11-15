package history

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
)

// Fixed derivation vectors, version 2.
//
// These are the vectors of the derivation research (work/hwid-kdf-poc testdata/vectors-v2.json, research/runs/2026-11-15/
// v0.8.0-hwid-preflight-safety section 2.2), which an independent .NET HKDF implementation reproduced from the written
// specification. Every input is synthetic: the identifiers spell WWLGTEST and WWLOGSTEST0001, and the RSA keys are test keys
// without any value. The vectors fix the derivation for good: a change of any constant, encoding or canonicalization rule
// fails them, in every build profile, and must become a new derivation version instead.

// syntheticProtocolKeyA is the canonical PKCS#1 DER of synthetic RSA test key A: 1191 bytes, key id 69a1a3e72059b8b4.
var syntheticProtocolKeyA = mustHex(
	"308204a30201000282010100d61108739399eeb01b65ffb32c16ac62ed28da0f48c28c5b9627aa835b2f2c67c9eb150ab682c8bf4d4424e5" +
		"2bf9d424bbf18d101904142ffb110af437eaf11a2575ebf126883a908e0f9eb0f1cbdf4bc4e7f5fba89ad78f41ce12cd7411ca76211148a7" +
		"fc5847ac66059a61cd98dc06582bd05fdc2f695b814aa8000e151260ac3c1da4859862d3a5fa3667b7c2d2ed5d29979744e393491f562357" +
		"b168e17784e68ca05aed508b22bdaa64ac1897f26f18a9e166bdde7609dfbc84fb4faf484d24b09fc2a54f38f7636007642967875d7def05" +
		"6db4be2ba28354a5518bd45de1e07e70799f50c635b3a5cb2e786900bf5deaeaa96e8abda0bdc57ddc9c023102030100010282010009766d" +
		"4a369f1a56fc8f10b389e262b268cb628044761d203b4dfb929ce843ed3e96beb25f88fe78be60a1c4e3a1c287f12ae7526bd8ab4fc7a864" +
		"349178383a30a7bacb4929c274fb62a3602d001719d15573a1313dcf038d241fdea71fd6344c97e7ff4cff5603bfeac57d474a57878c443b" +
		"8cd13de3496138bc9e6c00f073f2d433dadf5ef106fc53618b56e74d96c28b7317e85bbb8ae0c5c95d262fe1df6736959c503694962778c3" +
		"29b2ef56bbc02e09b68a0ca866de4b6c8077b3a32b6ca4b7577aa0b90c4e3420f0ebd915ee3124231d9908dd242df62bba4163affdcf5c11" +
		"00afd794dedc1689f3f4348fca67761d29a27283722160cfb176d919a502818100e2c71c3586559662ac73c605ec9012a2df27731f07c56d" +
		"4c4bbbfd2e649597164e790cbc85982516f4c298a5d8549a47f63e5c702002b8b862e48fc229e0602b05b42d449de72aeac9d96c61bca362" +
		"5f295a161e1d8592c8d446bffefe49359c019e1c7cd20656aa0906d22f05b6d764e40e3c01a18c1cd7535ced4d3b8defaf02818100f1a69b" +
		"8a1b1cf135624bdcebf5e946400d96308b5fe0bdece4828afe7b4dc3270f319a24e3209be1657b966eab60df4278b1d6fab5597632698a42" +
		"6bab8416334c8d6917fcb1cc8dde52cc3b1a22a064d13a5bb5e713243970cd2d4f89c9b336ee7f52f37ddd4f6f1111bd0865275e453c0714" +
		"1cefd5e3fd61b53a9b5436c41f02818100aefa20baefdec3fd78c08faf6c7b31d5f7c7119d84ba817dd805c1f174ecb33a23ec64b6658222" +
		"441730bae56408cb4df122e6e220e0d8b474a19486a5171a3cb7d6f102bbaf6d3ee1e5e0297d8ab75163afbdd718c242a2b09fb26ec6b7a8" +
		"24aa5edbee37836a852217e03f25f29139a8798f3556f7ac1d3c963c0756874ac30281804b0be3e81b0d7cf788446922baa72c92fde533c4" +
		"450d65ce691505978cb194b6cfa826e7e7c59bc37f21c7912d16139ff01dc2d6ef750e190fdfc94ae8e44784bcf479bbe255516289b60b09" +
		"1bcfbf22303c4079187f4053ba6d6df1f2a7ba4987fdf0c3ffc5b84294f97e4c5a25c4c819a15524bb7320aff325364340ff151f02818020" +
		"8aab5cee884266c958f4e72275667abd60d2133bf99b256b8d22933e2cb71677b09a85459861abdc5ecf472acdf1b30bb75b908ad44055a6" +
		"191e8dbcb7b0cd564fa2c557e6dd871acdecd919188d0bd46400a1e3bdb6d00b2fb2bb35481c10155c18b481298e2c434d83fba891040dd7" +
		"877b1d6759c8b240950652b23c1718")

// syntheticProtocolKeyB is the canonical PKCS#1 DER of synthetic RSA test key B: 1193 bytes, key id 6d867c9d8b8c266b.
var syntheticProtocolKeyB = mustHex(
	"308204a50201000282010100b04815297794f55962dd46bd09d2948fcc5ddd6a5822abe224a033a07f2d7040ba0e2d76597a3ffee8bc7740" +
		"a3168ee0ea008b1fda21e81d431f601d953f30ba1cc047a6d771cc815858e77c4df2bff99f299b188ff3d1e55fec3a5b3c22278d5ccbe5c0" +
		"80326097793b68e494d07e1fdea66c6916743c132997b718a79c44a79b80d40aa1a4c07f09b85b86927cbd051f713027df1acbfaa995d91b" +
		"bd38bcae1660e00f2aa6fbd57a1f7b14c2dae56e091258bf4d892e615616b52707fcad50e7d850a5c62c919ecbe2c543c5794c6cf0ee79e2" +
		"bbc6183cc75a3987f9429f3e728119d8c143194b299253c7959be921e4f190fce2e9e18aba3b2b6b281a52610203010001028201002e2a9a" +
		"9c330ca85ac209186b0ea4ff324fbfef6a57edb09d37a15b13f7eaa300d89938efcb4c38715e6b6941dcd45e13ea44bd2ad8da536b427677" +
		"9a17c9a64dbf94ddcf04927190185ea4354a46be5f35ff53f69150b6123fd6628c1d4a0698bf8a48bfeaf30697f4f089ae7f88c0d740dccd" +
		"fc919c5c555a69b8d274a6f8d58ab600592d7646bd355cd0ec0883efc76a95c4acf615ee870798720487510a1120d8f28b3a5d4523694e78" +
		"8abede84384cd8276b9ee4c7082ad3dc8974c720f993d26198001dfe91d35f0ce3fbcad9cf98dbd18b2f0ac9ff463157efd33da1bf1d58dc" +
		"0506ca71f7af6bf8423c62db07926993a47023c25c7b2eb2e33599792f02818100c2ec7d4bf822278404c7a6b5d7a69edc6c33de8d5d85c8" +
		"79af7a875862af79bd71154c336cf5df26396d82c376369e2daad8ead80220366c14f75ee70e5a8b1ea6db69a60c4440d3e0301fdeac2ba3" +
		"802c983b912992b53d9906ee7952ab19768702e604df838099fe0404b97b19027e1541b77a1bde4667ec9e2e25bf4586c702818100e7843c" +
		"a012d40a3bb2c37e5aa7391f25458aaa7ee8a9f0324a023cccc85b67e9a8bb6a27015ce84305279fed04671447e825c08218b6d5dcc02243" +
		"2933be0b3ef513a45fac7432569116bb55bda0e4c04f23feb82e1c8cf9a4ff6df8badeb6be92f2c0cf393e0f5f2ac5411d1579dd0ce5fd9a" +
		"48cc1944ba44a4872c2e2a95970281810095e5831d4ebe05215303adc21d0a77140ee97c75bdbbc9c53a1ac1c6486da4f2f7e966e60d1eae" +
		"f2182b8ce97f18d55f4d08eee01a1267878a4008ed2a6e9a9981bb2d6d91f552dd1c84259df94cbdb92d920c0cbfa9a052b4414e56622a4d" +
		"b48672c62c6d2e155156b4046616b1cee6e2b5dcc0bc799c2f03194967a1c577b5028181009e2b43d800c508b0bc72bf126408ac74a44c16" +
		"2b59fd2ff2cb8937cc985d8a62a23a584d89aad5ab640c2c409baf68b5afae8b14d699a3068be55c88fba6acbc386750f76434866789fc43" +
		"c4522767e0d2b0bec74a098b6ecb010f3acd40770d400bc51a4dfb4e3930594f20f5992a2233c6ccef4b31403919c865e9132958c5028181" +
		"008709b69c3693af9f9b7ff006a9916e6b956af1a2b4d05cd307c0e0500af1cb496a14c706760cfcb84324431e7ae0b8f65673cb58d482d1" +
		"3c338b4d75aafe6af8316c99e9954a15154963f94cdd40b54d6e7bbf3e5f93ca6668a590319b0f2cc23aa864f07d344f93ca05286bd33faa" +
		"75d06b1c7f015c986e87e87c01d380f70f")

type kdfVector struct {
	name, source, identifier string
	protocolKey              []byte
	keyID, key, check        string
}

var kdfVectors = []kdfVector{
	{name: "uuid-1-rsaA", source: sourceSystemUUID, identifier: "474c5757455454538000000000000001", protocolKey: syntheticProtocolKeyA, keyID: "69a1a3e72059b8b4",
		key:   "2abae3a3dcaa4848590e955546e2524448db49cb8c34504e83536f8e66a9d77679c0b71f4e586490109ed3f45bd4554f55d96ed5d6cc51e28d005ea6b237bd79",
		check: "2e85806d08c4fb62396f087fc5101306"},
	{name: "uuid-1-rsaB", source: sourceSystemUUID, identifier: "474c5757455454538000000000000001", protocolKey: syntheticProtocolKeyB, keyID: "6d867c9d8b8c266b",
		key:   "4be6760ba1a68a6f61d63fdb1d53c9b20f570456eb6dbffe3e901f16f3cb07f631433899ae2ce542b91c7ecb8debe9996a1fd77bfc067317619293592a6f0e31",
		check: "73def51611d60212b0ae0fddae0c6e36"},
	{name: "serial-1-rsaA", source: sourceBaseboardSerial, identifier: "57574c4f47535445535430303031", protocolKey: syntheticProtocolKeyA, keyID: "69a1a3e72059b8b4",
		key:   "319ff50dbbc602d88baca49b05763088dcc6694635d5b84cc211891ab7237f3a4bd79ab380c50fd41cff8928144017e48eb24ef68553fea54f024fe4a6029abf",
		check: "c36d8319a1062989cdb7547a2855f19a"},
}

// TestKeyDerivationVectors derives every fixed vector and checks the input encoding, the key, the check value and the
// protocol key id.
func TestKeyDerivationVectors(t *testing.T) {
	for _, vector := range kdfVectors {
		t.Run(vector.name, func(t *testing.T) {
			identifier := mustHex(vector.identifier)
			input, err := derivationInput(vector.source, identifier, vector.protocolKey)
			if err != nil {
				t.Fatal(err)
			}
			prefix := append([]byte("WWLOGS-HWID-IKM\x02"), byte(len(vector.source)))
			prefix = append(prefix, vector.source...)
			prefix = append(prefix, byte(len(identifier)>>8), byte(len(identifier)))
			prefix = append(prefix, identifier...)
			prefix = append(prefix, 0, 0, byte(len(vector.protocolKey)>>8), byte(len(vector.protocolKey)))
			if !bytes.Equal(input, append(prefix, vector.protocolKey...)) {
				t.Fatal("the input keying material is not the version 2 encoding")
			}
			key, check, err := deriveHistoryKey(vector.source, identifier, vector.protocolKey)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(key) != vector.key || hex.EncodeToString(check) != vector.check {
				t.Fatalf("derived key %x / check %x differ from the fixed vector", key[:8], check)
			}
			if id := protocolKeyID(vector.protocolKey); id != vector.keyID {
				t.Fatalf("protocol key id %s, want %s", id, vector.keyID)
			}
			// The same inputs give the same key every time.
			for i := 0; i < 50; i++ {
				again, againCheck, err := deriveHistoryKey(vector.source, identifier, vector.protocolKey)
				if err != nil || !bytes.Equal(again, key) || !bytes.Equal(againCheck, check) {
					t.Fatalf("derivation %d differs (%v)", i, err)
				}
			}
		})
	}
}

// TestEveryDerivationInputMatters changes one input at a time: the source, one bit of the identifier, the protocol key, the
// identifier length at the field boundary. Each gives another key and another check value.
func TestEveryDerivationInputMatters(t *testing.T) {
	base := kdfVectors[0]
	identifier := mustHex(base.identifier)
	key, check, err := deriveHistoryKey(base.source, identifier, base.protocolKey)
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(identifier)
	flipped[15] ^= 1
	for name, inputs := range map[string]struct {
		source      string
		identifier  []byte
		protocolKey []byte
	}{
		"another source":              {sourceBaseboardSerial, identifier, base.protocolKey},
		"one identifier bit":          {base.source, flipped, base.protocolKey},
		"another protocol key":        {base.source, identifier, syntheticProtocolKeyB},
		"identifier byte moved":       {base.source, identifier[:15], append([]byte{identifier[15]}, base.protocolKey...)},
		"protocol key byte truncated": {base.source, identifier, base.protocolKey[:len(base.protocolKey)-1]},
	} {
		other, otherCheck, err := deriveHistoryKey(inputs.source, inputs.identifier, inputs.protocolKey)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if bytes.Equal(other, key) || bytes.Equal(otherCheck, check) {
			t.Fatalf("%s gave the same key", name)
		}
	}
	for name, inputs := range map[string][3][]byte{
		"empty identifier":   {[]byte(base.source), nil, base.protocolKey},
		"empty protocol key": {[]byte(base.source), identifier, nil},
		"empty source":       {nil, identifier, base.protocolKey},
	} {
		if _, _, err := deriveHistoryKey(string(inputs[0]), inputs[1], inputs[2]); !errors.Is(err, errDerivationInput) {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
}

// TestProtocolKeyCanonicalForm feeds the same synthetic RSA key in every form a build might carry it, and other material
// that must be refused.
func TestProtocolKeyCanonicalForm(t *testing.T) {
	parsed, err := x509.ParsePKCS1PrivateKey(syntheticProtocolKeyA)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(parsed)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1PEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: syntheticProtocolKeyA})
	forms := map[string][]byte{
		"PKCS#1 PEM":                  pkcs1PEM,
		"PKCS#8 PEM":                  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}),
		"PKCS#1 DER":                  syntheticProtocolKeyA,
		"PKCS#8 DER":                  pkcs8,
		"CRLF line endings":           []byte(strings.ReplaceAll(string(pkcs1PEM), "\n", "\r\n")),
		"comment line and blank tail": []byte("# synthetic test key\r\n" + string(pkcs1PEM) + "\n\n\t "),
		"no final newline":            bytes.TrimRight(pkcs1PEM, "\n"),
	}
	for name, form := range forms {
		canonical, err := canonicalProtocolKey(bytes.Clone(form))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(canonical, syntheticProtocolKeyA) {
			t.Fatalf("%s gives another canonical form", name)
		}
	}
	if canonical, err := canonicalProtocolKey(syntheticProtocolKeyB); err != nil || bytes.Equal(canonical, syntheticProtocolKeyA) {
		t.Fatalf("another key: %v", err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	broken := bytes.Clone(syntheticProtocolKeyA)
	broken[len(broken)-200] ^= 0x01
	for name, material := range map[string][]byte{
		"an EC key":            pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}),
		"not a key":            pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("not a key")}),
		"a damaged key":        broken,
		"nothing":              nil,
		"a public key, PKCS#1": x509.MarshalPKCS1PublicKey(&parsed.PublicKey),
	} {
		if _, err := canonicalProtocolKey(material); err == nil {
			t.Fatalf("%s was accepted as the protocol key", name)
		}
	}
}
