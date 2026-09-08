// kdfvec is the reference generator for testdata/kdf-vectors.json.
//
// It implements FORMAT.md §3 (with §3.1's K_P) and the secrets records of §7.6 exactly as
// written, using fixed inputs, and emits every
// intermediate value so that an independent implementation can be diffed against it stage
// by stage rather than only at the end. Every wrong reading of the spec still produces 32
// plausible bytes; only a vector notices.
//
// It also writes testdata/kdf-inputs.json — inputs alone, no outputs — which is what an
// independent implementer is given. ML-KEM encapsulation is randomised, so the ciphertexts
// it produces are recorded there as inputs; everyone decapsulates the same ct.
//
// Run from the repository root:  go run ./tools/kdfvec
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/mlkem"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// ---- fixed inputs -----------------------------------------------------------------------
//
// Recognisable byte patterns, so nobody mistakes them for real key material.

func seq(start byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

var (
	vaultID     = seq(0x01, 16)
	recipientID = seq(0x11, 16)
	slotSalt    = seq(0x21, 32)
	argonSalt   = seq(0x41, 32)
	vmk         = seq(0x61, 32)
	archiveID   = seq(0x81, 16)
	archiveKey  = seq(0x91, 32)
	wrapNonce   = seq(0xB1, 12)
	wrapAAD     = []byte("Enfold kdf-vectors: placeholder AAD, not a real slot record")

	// The slot region header's entangle_salt (§6): the vault's, 16 bytes, and the salt of K_P.
	entangleSalt = seq(0x71, 16)
	// One pinned nonce per secrets record (§7.6, R22): three records under one key must never
	// share a nonce, in a vector file as in a vault, and pinning them is what makes the
	// records reproducible.
	secretNonceEscrow    = seq(0xA1, 12)
	secretNonceEntangled = seq(0xF1, 12)
	secretNonceHistory   = seq(0x51, 12)

	// P-256 scalars for the hardware slot. Any 32-byte value in [1, n-1] is valid; these are
	// far below n.
	skHW = seq(0xC1, 32)
	esk  = seq(0xD1, 32)

	// X25519 ephemeral scalar used when wrapping the recovery and password slots.
	ephX = seq(0xE1, 32)

	// 128-bit recovery key. Chosen so that the digit rendering (R11) exercises a leading zero.
	recoveryR = []byte{
		0x00, 0x10, 0xFF, 0xFF, 0x34, 0x12, 0x00, 0x00,
		0xAB, 0xCD, 0x01, 0x00, 0x80, 0x7F, 0xFE, 0xCA,
	}

	passwordASCII = "correct horse battery staple"
	// The same passphrase given in NFD (e + U+0301) and NFC (U+00E9). R4 requires both to
	// derive the same key.
	passwordNFD = "café au lait"
	passwordNFC = "café au lait"

	vmkGeneration uint64 = 7

	// ML-KEM encapsulation is randomised, so the ciphertexts are NOT recomputed on each run:
	// a fresh ct would change K_k and everything downstream, and the vector file would stop
	// being reproducible. These were produced once and are now fixed inputs; everyone --
	// including this generator -- decapsulates them. To regenerate deliberately, replace both
	// strings and re-run; any independent implementation must then be re-run too.
	recoveryCT = "ef5b766da03150408a565926e23bed107589c165f7463c981e87ca7263ff6a81dc5739ebb3b11b0e23e69bd25b1f87211de2fc1ff0bdf5d1b943b421a036830327327b96bc9e66d20e28395506f976a20732fde050aa1cf0074a01ff2d47d1a7e332933b86c2ed6b98e735ff63899beb095a65ebbf8262a8ab74d750511f48584077e87751c7106b3c7cac0d6ffc54cb17cfb24737a8bd84fbd1753362bcc8709cababf224ddb483192af444299f697944564838fc0f899b9379d2e3281259ee6bc8deed181ac4d5a293e9356e94f69335f063accba9c35ab9c1b09bd02ad164d8d98f5ca7432cc44c44f0d1ef04d4bb34b60cd9de2a1deb8738c67de8acfb93d653f8f40b65341273c32b4de4b5cc0d4e2aef647fd5e0cd15d97220ec646c81f47099936c2db23cbd0d153799d098b0cb34e6da9299a85e894d09df49715225579052bd9cf49c2e41063a4f299a130c1a6b90308c932889811340067a1bd342880bc64079b939b396c84f68cea1a27c74f6ca8421d9eb5b37f474540b65b8ba305ffbc5e048a2193c9cbe75c2e0b204b3735dfa7b3ad04c002c1a609f75114e4783bdfb2880cb73f9a2017b280a20247f71fb870e4f17713462493d48ef358732cd80905d3099f7cfcf799f175598e1a2d89524455874d0018e63ce1b75e65db0b137c26b94c8447aa55865342c15c5cad03ad5bced934000666e340e9994e731d083544afef49e6f5912cf21be04621a79432c5c06a9c2bf24d70ebdf426089ef96972fa3abdaaaa0ca2b7d80cd327c0c7a80dfe3d94b0bd1973fa038ced13368af1ca890134511d361a5bb7005cd9947417dbea09d412a54a2d99ccb429e7750e718adce0e103cb0b862b247f485e6cb7c3c9fb5a743b2ba1abf163ae1e85ebcae057e801e0c7292f611692569d6c04bc8233580516d361465ebc1b51908b2409ebd17326723c753ec09bfa4473cfb55ddb1f4200f207e691446046ea53fd76cfc242c86b3dbf56fdef347560e0553f31ea174774aa30ee9f3e4771c077c4598cf598509c695ab83424469ec62bff821bf97a39f049861e4b50922c3f8bfb34ea6baf52d2da7b69a448d92c56f7f2f930756d10ab2b745896a00d2dc40d532d6ed83d29598435d1d32ccd69dd6f3a1f3ea7f7062ce2bda65ffb19d4fc2339b52c5712ea4b99c57cfa83bfc0e6f0348f43a76eb5a8659ae8d9b917f7d44e91cebc75ac278be63dc2f6bb2e73d5e44fa3245b347b76323c77cd749ae7d7e3bdf843734b054f30aca9a61add2992bb77ab4a6c51ed347a2761e2cecc11093dd7310ea7773d5f23914824a1cd8f268fc3f91ed644036e1dcc6fe8a19566aa9bea9feab82f14a55957ee1f168e564d613ae297e0dd295b461f9289da67ce0c3898b4f7c5cc4e860cf85552045e317ec1c99f52e606c08f80ecafeef64a9fc9c6a9b6218046d53a74daf1457c3ff75b6d8c72ce3289276da32b9201b7e1f7a2dda5c9624a24dc37b482d6a2d9d8804647dbc6f581606893a7372431ed9a492e3da944048f684de0b6be0a8b0cb32c3d9062c8fe8646447d60afe82d4124494f6fe09d24be574bb95fe2e423aa81493b6b13e4dab701d014ecac7e6504c41aaa059b804ff956f52af153e9e50774f3e1d68f85c97fb559ec521e6785ccf85250337bafcf17c9348fb7b5c38f3a8194513ec8ad1d3efb3bb7068b523f72c81f878a76b19bf5b55f25cd387eedb8dadd41433795d032b3d72ee8c740f7613379cd61de7403786e3ddfee05b26333a9e3dbd7e5243ffd0b6fc5a052fa44b4916353607ff114520035c29a87b14f3850564194e52cdcf46113254ea4977186619e2d72cea28a3832cecffd96a5c093cd69ac9fd5baf086ce0c27aa95f59b427bdf9f782f2ee24de37a093ee390f3b5b81136909049f48f3a967181794f520d70595e51ba97a14c50f8a121ff6583c14f04bab23da1582022f2866d243b74d291114f1479158b6143fefd3fafbc6186a4ebb1b8f8980ba580b9f9aa89a680e2ae6156eb11cccec301cc0db04abb2ca2961662ad74c1bd6e6a8d400bcc75713a4d2e90fce18f78297c840b10ead7852e74e1343ba9ef9117b80a28a65c57c6eee0de40366969fdb088eacbfb54147efa95ff590ad00f79640b6d23ac67a9054a41c47f4c81c77b693c6aef00a4831e15fd89fa5888b777322cf368cad8cf3f9f6974c09b42"
	passwordCT = "71701ef005f4aa5dba4ff8332ed65719fff7f6fb0e8c47b7a58ef5d127f424c8b7ac8d3e926706636cc466e57d49520d431f961677e099b9ccfab70db868cc597fcddc7ffa26ad32f0edf7af75253b971010f7a0d030fd5741107c1e02eafe65df6c4ce61b31cda1c8d30d8a0b8b8b75a71aeb4b46c7df039f60751ee50ae1c7b5a39193eddd1b62d1395c6acb7eee33f078c19405c2baccc905152bc609968a32f89f6a41c0705755bf2d6ba1cc874d4547705b06a894c2571cb4f03dededffcfff58f3590a1d7d8337e75ad9c80ad7d656369bf405274d540b96d8cec9cab697dc885c3729aad0d650ef4951c1514d6acb1c742f0e68845318d9f01728c4fb407012610077b68e56e14e2c1e3ddd21521cc846b571da8fbe36e167c117d90511d5f3ce4f03214188f44829ebe7d9bf5ba86b8ed54b6c55ff529b38bdef8ba32d59b03d5185c3bfc70e3a74240a6202442ad7e49f5abc3cf36c23b5050a70865c4f366dd4faa6e2c3f60f975799f4b9c2826b5a200e72724364bf7025e934edfaa6d73589c19a4fc156467ffe5768f1f5e2b8ccaae350dae20084fc4e1cbc698cdf5edce08cfb3a60c74fd5a7e284ab73f47db12325096855fdd5d582baee32e4382e2ea856d0cd1e7868159febe6ca28c7806624f0e150fb3b9241bf737ff9ab77d211785a0422156d80433f23a0047f78069cbdf0dfc026ef76859ac5ed3ddc1b75c73d21134cd0bd28192ff57290cb5d98faa2607b07852b95870787b58bca400f7600455a5b2c2c2d2f2dd54f39ff4e2c94139240ad8f1d6d5abd1920bdab491dde48ff9dbcd1a291029ce4c77d146013cf258a834a3d69d940c671cecbdd824e4cf4a79b10814eb10ce2af48939674fa9c96fff6414a5ca5b02e5c09c7d70995d88e7554feeae5ed6acb70bcb2a343f5187fab2191f4054359b0eb1ad7988c5a8666f2880f4a6fe600d1afb0261cf4f2a563fea414286b821add65c45a3e647e4b6d4c71d5c460eef534a4c444e67f5d0a746b2960a701029dddd49f2a747d0d8270e7edeebdba01f65ae8b24e34302c7c4698953d93f822f0fc74128270b6a5a2e88b181848e1f2d3de9242126a1d5f50ecf56cc3578a4775c024d5a8bd848e8e19045f2a9bf16264bcb23d913fc27774ea065a3451ca9c7ab26ece1c5ed93f8aecc0d21a8f3dde0bf629fcf4cb26ff749dcb71b9f9e6f988a450c48f32be6f7764a8be7c982c10aced616eed9803c25aa92feb064f1542b6b4dde98cde0cf356229a6f4b8fed2785dcde45d7fbb6eb618aea56d67ae94d3a6c32f7df912b3afb614619b16feb2943202ab2db627175b91bc440c7f3a71f6ab8d68e3a1a126af38cdc91679ccb3ac973b35a89d9779532628f48939ab6c79148646375bcbedee350c44d8ab6cefb95f25d328cbc4f12aeaa02e4822a39a8846870a64e7a7e1c8a13272b78f329f511bc3aa4e6bb5a6d6cef7f1bdbe7c01b7a0a15a3363ff362b29fa013108a83c5d849079bdd2a9d6cbda019883b3e6e3984cbbddd7b5a9b9ba18aeb0efc9c43eede69c85e6a501363148140e5500aadb1e6b7f3f0333e9b600b7d895a11fb091f4f2be05a75781311d1de4493729f001a6bbd190e0bc2904222a32e363db9c71f290e72e42765182af73800ad0fb44b03e7c89115240965dd08a534ba72a0a8e5df704132ce8da0905fe22b2d35ace6e277f473ac09e21d60feacaf3ec41183a1a644855c2b8ce9fc451873ca1c2dba9dee8b478984d42b630bc080f2b788ee93c7781ee485e79557e9ffbb1af51bd592b1b5f4f109ac7940b055394994c62e7563e9b8f87fe79e4033c0eaae0c66da3dad0e1eb0fe74db9b7988f684f45b46118c3a74b82ca54f7bf0d919978ab15cad8851304ff6efe3f52f1246c7c3adb6e6792ed8d1c78d670804f0b6c01815e10159d93cce63da9a96ebc2b4ec16939f99b73250377bf5247b362c3f99dfca88eed5ec854538afdb859354165f9f30c878b42613ab6a24971accec57d13983c8d69401ea396d387c78bfed4a1b5cadb7f8db5b071c6838fdd7c0fe886836f149e0c2b706293aa124782352ec38a7a4431d351b8715c2ba633d511293bf9ac405daf8896ac8958a507d61c43a54e6bff4ea6fe18bea972794a0fd3b22e96368e71343ce8f3f5e3689e0e82eef3ea5ceded19abb15ffb98c7ff9bf8efaa13f5d93d68752d88456"
)

// Argon2id parameters. "tiny" keeps the vector file fast to check; "anchor" is one vector at a
// production-shaped size so the function is also pinned at realistic memory.
type argonParams struct {
	MemKiB  uint32 `json:"m_kib"`
	Time    uint32 `json:"t"`
	Threads uint8  `json:"p"`
}

var (
	argonTiny   = argonParams{MemKiB: 8 * 1024, Time: 1, Threads: 4}
	argonAnchor = argonParams{MemKiB: 512 * 1024, Time: 2, Threads: 4}
)

// ---- primitives, written to match FORMAT.md §3.3 rule by rule ----------------------------

// R1, R2: HKDF-SHA256 Extract-then-Expand; nil salt means zero-length; raw concatenation.
func hkdfN(ikm, salt []byte, info []byte, n int) []byte {
	out, err := hkdf.Key(sha256.New, ikm, salt, string(info), n)
	if err != nil {
		panic(err)
	}
	return out
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// R3: info = ASCII prefix ‖ vault_id ‖ recipient_id, no separators.
func infoSlot(prefix string) []byte    { return cat([]byte(prefix), vaultID, recipientID) }
func infoVault(prefix string) []byte   { return cat([]byte(prefix), vaultID) }
func infoArchive(prefix string) []byte { return cat([]byte(prefix), archiveID) }

// R4: UTF-8 of the NFC-normalised string.
func passwordBytes(s string) []byte { return []byte(norm.NFC.String(s)) }

// R6: Argon2id v0x13, m in KiB, threads = p, 32 bytes out.
func argon2id(pwd, salt []byte, p argonParams) []byte {
	return argon2.IDKey(pwd, salt, p.Time, p.MemKiB, p.Threads, 32)
}

// R13: salt' of the standalone password slot. Since Revision 2 no hardware slot has one.
func saltPrime() []byte {
	s := sha256.Sum256(cat(argonSalt, vaultID, recipientID))
	return s[:]
}

// §3.1: vault_salt' = SHA-256(entangle_salt ‖ vault_id), the Argon2id salt of K_P. No
// recipient_id — the entangled password is the vault's, not a slot's (§18.1).
func vaultSaltPrime() []byte {
	s := sha256.Sum256(cat(entangleSalt, vaultID))
	return s[:]
}

// §7.6, R22: a secrets record's AAD, 20 + 16 + 1 + 16 = 53 bytes.
func secretAAD(kind byte, id []byte) []byte {
	aad := cat([]byte("Enfold/v1/aad/secret"), vaultID, []byte{kind}, id)
	if len(aad) != 53 {
		panic("secrets AAD must be 53 bytes")
	}
	return aad
}

// §7.6: one secrets record, sealed under KWK_secrets with that record's pinned nonce.
func sealSecret(kek, nonce, pt, aad []byte) []byte {
	if len(pt) != 32 {
		panic("a secrets plaintext is 32 bytes, padded where the secret is shorter")
	}
	g := must(cipher.NewGCM(must(aes.NewCipher(kek))))
	ct := g.Seal(nil, nonce, pt, aad)
	if len(ct) != 48 {
		panic("a secrets ciphertext is 48 bytes")
	}
	if !bytes.Equal(must(g.Open(nil, nonce, ct, aad)), pt) {
		panic("secrets round trip failed")
	}
	return ct
}

// R11: 16 bytes → 8 little-endian u16 → each ×11 → 6 digits → joined by '-'.
func recoveryDigits(r []byte) string {
	g := make([]string, 8)
	for i := 0; i < 8; i++ {
		v := binary.LittleEndian.Uint16(r[2*i:])
		g[i] = fmt.Sprintf("%06d", uint32(v)*11)
	}
	return strings.Join(g, "-")
}

func recoveryFromDigits(s string) []byte {
	parts := strings.Split(s, "-")
	if len(parts) != 8 {
		panic("recovery key: expected 8 groups")
	}
	r := make([]byte, 16)
	for i, p := range parts {
		if len(p) != 6 {
			panic("recovery key: group must be 6 digits")
		}
		n, err := strconv.Atoi(p)
		if err != nil || n >= 720896 || n%11 != 0 {
			panic("recovery key: bad group " + p)
		}
		binary.LittleEndian.PutUint16(r[2*i:], uint16(n/11))
	}
	return r
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// ---- output ----------------------------------------------------------------------------

type H map[string]any

func hx(b []byte) string { return hex.EncodeToString(b) }

func main() {
	inputs := H{}
	vec := H{}

	// Everything shared.
	inputs["vault_id"] = hx(vaultID)
	inputs["recipient_id"] = hx(recipientID)
	inputs["slot_salt"] = hx(slotSalt)
	inputs["argon2_salt"] = hx(argonSalt)
	inputs["argon2_tiny"] = argonTiny
	inputs["argon2_anchor"] = argonAnchor
	inputs["vmk"] = hx(vmk)
	inputs["vmk_generation"] = vmkGeneration
	inputs["archive_id"] = hx(archiveID)
	inputs["archive_key"] = hx(archiveKey)
	inputs["wrap_nonce"] = hx(wrapNonce)
	inputs["wrap_aad"] = hx(wrapAAD)

	inputs["entangle_salt"] = hx(entangleSalt)
	inputs["secret_nonce_escrow"] = hx(secretNonceEscrow)
	inputs["secret_nonce_entangled"] = hx(secretNonceEntangled)
	inputs["secret_nonce_history"] = hx(secretNonceHistory)

	infoIK := infoSlot("Enfold/v1/IK")
	vec["info_IK"] = hx(infoIK)
	infoEntangle := infoSlot("Enfold/v1/entangle") // 18 + 16 + 16 = 50 bytes (R2, R3)
	vec["info_entangle"] = hx(infoEntangle)
	sp := saltPrime()
	vec["salt_prime"] = hx(sp)
	vsp := vaultSaltPrime()
	vec["vault_salt_prime"] = hx(vsp)

	// ---- hardware slot ------------------------------------------------------------------
	p256 := ecdh.P256()
	skHWKey := must(p256.NewPrivateKey(skHW))
	eskKey := must(p256.NewPrivateKey(esk))
	inputs["hw_sk"] = hx(skHW)
	inputs["hw_esk"] = hx(esk)

	pkHW := skHWKey.PublicKey().Bytes()
	epk := eskKey.PublicKey().Bytes()
	hFromToken := must(skHWKey.ECDH(eskKey.PublicKey()))
	hFromHost := must(eskKey.ECDH(skHWKey.PublicKey()))
	if !bytes.Equal(hFromToken, hFromHost) {
		panic("ECDH asymmetry")
	}
	hw := H{
		"pk_hw": hx(pkHW),
		"epk":   hx(epk),
		"H":     hx(hFromToken), // R5: 32-byte X coordinate
	}

	// (a) no entangled password: pre = H
	hw["no_password"] = H{
		"pre": hx(hFromToken),
		"IK":  hx(hkdfN(hFromToken, nil, infoIK, 32)),
	}

	// (b), (c), (d): the vault has an entangled password (§3.1, §18.1). K_P is Argon2id over
	// the password alone with vault_salt' as its salt — no H, no recipient_id — and the token
	// enters afterwards through the HKDF, IKM = H ‖ K_P in that order (R3, R5).
	entangled := func(P []byte, p argonParams) (kp, ikm, pre []byte) {
		kp = argon2id(P, vsp, p)
		ikm = cat(hFromToken, kp)
		pre = hkdfN(ikm, nil, infoEntangle, 32)
		return
	}

	// (b) entangled password, tiny params
	{
		P := passwordBytes(passwordASCII)
		kp, ikm, pre := entangled(P, argonTiny)
		hw["password_tiny"] = H{
			"password":      passwordASCII,
			"password_utf8": hx(P),
			"argon2":        argonTiny,
			"K_P":           hx(kp),
			"entangle_ikm":  hx(ikm),
			"pre":           hx(pre),
			"IK":            hx(hkdfN(pre, nil, infoIK, 32)),
		}
	}

	// (c) NFC: NFD input must derive identically to NFC input
	{
		pNFD := passwordBytes(passwordNFD)
		pNFC := passwordBytes(passwordNFC)
		if !bytes.Equal(pNFD, pNFC) {
			panic("NFC normalisation failed")
		}
		kp, ikm, pre := entangled(pNFC, argonTiny)
		hw["password_nfc"] = H{
			"password_nfd_input_utf8": hx([]byte(passwordNFD)),
			"password_nfc_input_utf8": hx([]byte(passwordNFC)),
			"password_normalised":     hx(pNFC),
			"argon2":                  argonTiny,
			"K_P":                     hx(kp),
			"entangle_ikm":            hx(ikm),
			"pre":                     hx(pre),
			"IK":                      hx(hkdfN(pre, nil, infoIK, 32)),
		}
	}

	// (d) one production-sized anchor
	{
		P := passwordBytes(passwordASCII)
		kp, ikm, pre := entangled(P, argonAnchor)
		hw["password_anchor"] = H{
			"password":     passwordASCII,
			"argon2":       argonAnchor,
			"K_P":          hx(kp),
			"entangle_ikm": hx(ikm),
			"pre":          hx(pre),
			"IK":           hx(hkdfN(pre, nil, infoIK, 32)),
		}
	}
	vec["hardware_slot"] = hw

	// K_P at the tiny parameters is the one the secrets section below keeps (§7.6 kind 2).
	kpTiny := argon2id(passwordBytes(passwordASCII), vsp, argonTiny)

	// ---- hybrid slots (recovery, standalone password) ------------------------------------
	x25519 := ecdh.X25519()
	ephKey := must(x25519.NewPrivateKey(ephX))
	inputs["hybrid_eph_x25519_sk"] = hx(ephX)

	hybrid := func(kind string, ikm []byte, ctHex string) (H, string) {
		seedX := hkdfN(ikm, slotSalt, infoSlot("Enfold/v1/"+kind+"/x25519"), 32)
		seedK := hkdfN(ikm, slotSalt, infoSlot("Enfold/v1/"+kind+"/mlkem"), 64)

		skX := must(x25519.NewPrivateKey(seedX)) // R9
		pkX := skX.PublicKey().Bytes()
		dk := must(mlkem.NewDecapsulationKey1024(seedK)) // R9
		ek := dk.EncapsulationKey()

		// wrap (offline): fresh X25519 ephemeral; the ML-KEM ciphertext is the pinned constant
		E := ephKey.PublicKey().Bytes()
		hxHost := must(ephKey.ECDH(must(x25519.NewPublicKey(pkX))))
		ct := must(hex.DecodeString(ctHex))
		if len(ct) != 1568 {
			panic("pinned ML-KEM ciphertext has wrong length: " + kind)
		}

		// unlock: recover the secrets from the private halves. Decapsulation is the only
		// ML-KEM operation here; its output is what the original wrap must have produced.
		hxUser := must(skX.ECDH(must(x25519.NewPublicKey(E))))
		kk := must(dk.Decapsulate(ct))
		if !bytes.Equal(hxHost, hxUser) {
			panic("X25519 round-trip failed: " + kind)
		}

		pre := hkdfN(cat(hxHost, kk), nil, infoSlot("Enfold/v1/"+kind+"/combine"), 32) // R10
		return H{
			"seed_x":      hx(seedX),
			"seed_k":      hx(seedK),
			"pk_x":        hx(pkX),
			"mlkem_ek":    hx(ek.Bytes()),
			"E":           hx(E),
			"H_x":         hx(hxHost),
			"mlkem_ct":    hx(ct),
			"K_k":         hx(kk),
			"combine_ikm": hx(cat(hxHost, kk)),
			"pre":         hx(pre),
			"IK":          hx(hkdfN(pre, nil, infoIK, 32)),
		}, hx(ct)
	}

	// recovery slot
	{
		digits := recoveryDigits(recoveryR) // R11
		if !bytes.Equal(recoveryFromDigits(digits), recoveryR) {
			panic("recovery digit round-trip failed")
		}
		inputs["recovery_R"] = hx(recoveryR)
		inputs["recovery_digits"] = digits
		rec, ct := hybrid("recovery", recoveryR, recoveryCT)
		rec["R"] = hx(recoveryR)
		rec["digits"] = digits
		vec["recovery_slot"] = rec
		inputs["recovery_mlkem_ct"] = ct
	}

	// standalone password slot (R8)
	{
		P := passwordBytes(passwordASCII)
		A := argon2id(P, sp, argonTiny)
		pw, ct := hybrid("password", A, passwordCT)
		pw["password"] = passwordASCII
		pw["password_utf8"] = hx(P)
		pw["argon2"] = argonTiny
		pw["A"] = hx(A)
		vec["password_slot"] = pw
		inputs["password_mlkem_ct"] = ct
	}

	// ---- VMK- and archive-level keys -----------------------------------------------------
	kwkSecrets := hkdfN(vmk, nil, infoVault("Enfold/v1/wrap/secrets"), 32)
	vec["vmk_keys"] = H{
		"metadata_key": hx(hkdfN(vmk, nil, infoVault("Enfold/v1/metadata"), 32)),
		"db_key":       hx(hkdfN(vmk, nil, infoVault("Enfold/v1/db"), 32)),
		"KWK":          hx(hkdfN(vmk, nil, infoVault("Enfold/v1/wrap/archive"), 32)),
		"KWK_identity": hx(hkdfN(vmk, nil, infoVault("Enfold/v1/wrap/identity"), 32)),
		"KWK_secrets":  hx(kwkSecrets),
	}

	// ---- secrets section (§7.6, R22, R38) ------------------------------------------------
	//
	// One record of each kind, all three under the pinned nonce so that the ciphertexts are
	// reproducible; a live write draws a fresh nonce per record. The kind-3 record is the one
	// a rotation away from generation 7 would append: its id is the generation the retiring
	// VMK held, and its plaintext is that VMK — here the inputs' vmk, sealed under the
	// vault's current KWK_secrets rather than the successor's, since only the current VMK
	// exists in this file.
	{
		record := func(kind byte, id, nonce, pt []byte) H {
			aad := secretAAD(kind, id)
			return H{
				"kind":       kind,
				"id":         hx(id),
				"nonce":      hx(nonce),
				"aad":        hx(aad),
				"plaintext":  hx(pt),
				"ciphertext": hx(sealSecret(kwkSecrets, nonce, pt, aad)),
			}
		}
		historyID := make([]byte, 16)
		binary.LittleEndian.PutUint64(historyID, vmkGeneration)
		vec["secrets"] = H{
			"_note": "One record per kind, each under its own pinned nonce (a live write draws a fresh one per record, R22; no two records under one key ever share a nonce). Sealed under KWK_secrets of the inputs' vmk.",
			// kind 1: R ‖ sixteen zero bytes, keyed by the recovery slot's recipient_id.
			"recovery_escrow": record(1, recipientID, secretNonceEscrow, cat(recoveryR, make([]byte, 16))),
			// kind 2: K_P, id all zero — at most one per vault.
			"entangled_key": record(2, make([]byte, 16), secretNonceEntangled, kpTiny),
			// kind 3: a retired VMK, id the little-endian generation in bytes 0-7.
			"vmk_history": record(3, historyID, secretNonceHistory, vmk),
		}
	}
	vec["archive_keys"] = H{
		"index_key": hx(hkdfN(archiveKey, nil, infoArchive("Enfold/v1/archive/index"), 32)),
		"wrap_key":  hx(hkdfN(archiveKey, nil, infoArchive("Enfold/v1/archive/wrap"), 32)),
	}

	// ---- wrapped_vmk (R12) ---------------------------------------------------------------
	{
		ik := hkdfN(hFromToken, nil, infoIK, 32) // the no-password hardware IK
		pt := make([]byte, 40)
		copy(pt, vmk)
		binary.LittleEndian.PutUint64(pt[32:], vmkGeneration)
		block := must(aes.NewCipher(ik))
		gcm := must(cipher.NewGCM(block))
		wrapped := gcm.Seal(nil, wrapNonce, pt, wrapAAD)
		if len(wrapped) != 56 {
			panic("wrapped_vmk must be 56 bytes")
		}
		back := must(gcm.Open(nil, wrapNonce, wrapped, wrapAAD))
		if !bytes.Equal(back, pt) {
			panic("unwrap failed")
		}
		vec["wrapped_vmk"] = H{
			"IK":          hx(ik),
			"plaintext":   hx(pt),
			"nonce":       hx(wrapNonce),
			"aad":         hx(wrapAAD),
			"wrapped_vmk": hx(wrapped),
		}
	}

	inputs["password_ascii"] = passwordASCII
	inputs["password_nfd_input"] = passwordNFD
	inputs["password_nfc_input"] = passwordNFC
	inputs["_note"] = "Inputs only. Implement docs/FORMAT.md §3 (including §3.1's K_P) + §3.3 and the secrets section of §7.6, and emit the same keys as kdf-vectors.json. argon2_salt is the standalone password slot's alone (R13); entangle_salt is the vault's, the salt of K_P. The three secrets records are: kind 1 keyed by recipient_id over recovery_R padded to 32; kind 2 over K_P at argon2_tiny with an all-zero id; kind 3 over vmk with vmk_generation little-endian in bytes 0-7 of its id. All three are sealed under KWK_secrets, each with its own nonce (secret_nonce_escrow, secret_nonce_entangled, secret_nonce_history)."

	vec["_spec"] = "docs/FORMAT.md §3 and §3.1, rules R1–R13 in §3.3 (R7 retired in Revision 2), the secrets records of §7.6 with R22's AAD"
	vec["inputs"] = inputs

	write := func(path string, v any) {
		f := must(os.Create(path))
		defer f.Close()
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			panic(err)
		}
	}
	write("testdata/kdf-inputs.json", inputs)
	write("testdata/kdf-vectors.json", vec)
	fmt.Println("wrote testdata/kdf-inputs.json and testdata/kdf-vectors.json")
}
