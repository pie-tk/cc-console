package monitor

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

// 测试用 minisign 密钥/签名构造器 —— 自包含，不依赖外部 minisign 二进制。
// 格式与 minisign.go 里的二进制布局严格一致。

// makeMinisignKeyPair 生成一对 ed25519 密钥，返回 minisign 两行公钥文本 + 私钥。
func makeMinisignKeyPair(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	keynum := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	block := append(append([]byte{0x45, 0x64}, keynum...), pk...) // "Ed" + keynum + pk
	return "untrusted comment: test public key\n" + base64.StdEncoding.EncodeToString(block), sk
}

// signMinisignED 用私钥对 message 做 ED(blake2b 预哈希)签名，返回两行签名文本。
func signMinisignED(sk ed25519.PrivateKey, message []byte) string {
	keynum := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	digest := blake2b.Sum512(message)
	sig := ed25519.Sign(sk, digest[:])
	block := append(append([]byte{0x45, 0x44}, keynum...), sig...) // "ED" + keynum + sig
	return "untrusted comment: test signature\n" + base64.StdEncoding.EncodeToString(block)
}

// signMinisignFileText 返回近似真实 .minisig 的四行文本。
// 第 4 行故意使用不同于文件签名的另一段 base64，确保解析时确实取首个签名行。
func signMinisignFileText(sk ed25519.PrivateKey, message []byte) string {
	keynum := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	fileDigest := blake2b.Sum512(message)
	fileSig := ed25519.Sign(sk, fileDigest[:])
	fileBlock := append(append([]byte{0x45, 0x44}, keynum...), fileSig...)

	commentDigest := blake2b.Sum512([]byte("trusted comment payload"))
	commentSig := ed25519.Sign(sk, commentDigest[:])
	commentBlock := append(append([]byte{0x45, 0x44}, keynum...), commentSig...)

	return "untrusted comment: test signature\n" + base64.StdEncoding.EncodeToString(fileBlock) +
		"\ntrusted comment: test trusted comment\n" + base64.StdEncoding.EncodeToString(commentBlock)
}

func TestVerifyMinisign_Valid(t *testing.T) {
	pubText, sk := makeMinisignKeyPair(t)
	msg := []byte("hello cc-console update")
	if err := VerifyMinisign(pubText, signMinisignED(sk, msg), msg); err != nil {
		t.Fatalf("合法签名应校验通过，却失败: %v", err)
	}
}

func TestVerifyMinisign_FourLineSignature(t *testing.T) {
	pubText, sk := makeMinisignKeyPair(t)
	msg := []byte("hello cc-console update")
	if err := VerifyMinisign(pubText, signMinisignFileText(sk, msg), msg); err != nil {
		t.Fatalf("标准四行签名应校验通过，却失败: %v", err)
	}
}

func TestVerifyMinisign_TamperedMessage(t *testing.T) {
	pubText, sk := makeMinisignKeyPair(t)
	msg := []byte("hello cc-console update")
	sig := signMinisignED(sk, msg)

	tampered := append([]byte(nil), msg...)
	tampered[0] ^= 0xff
	if err := VerifyMinisign(pubText, sig, tampered); err == nil {
		t.Fatal("篡改后的消息应校验失败，却通过")
	}
}

func TestVerifyMinisign_WrongKey(t *testing.T) {
	_, sk1 := makeMinisignKeyPair(t)
	pubText2, _ := makeMinisignKeyPair(t) // 另一对密钥（keynum 相同，pk 不同）
	msg := []byte("hello")
	sig := signMinisignED(sk1, msg)
	if err := VerifyMinisign(pubText2, sig, msg); err == nil {
		t.Fatal("用错误公钥应校验失败，却通过")
	}
}

func TestVerifyMinisign_KeynumMismatch(t *testing.T) {
	pubText, sk := makeMinisignKeyPair(t) // 公钥 keynum = 1..8
	msg := []byte("hello")
	keynum := []byte{9, 9, 9, 9, 9, 9, 9, 9} // 故意不同的 keynum
	digest := blake2b.Sum512(msg)
	sig := ed25519.Sign(sk, digest[:])
	block := append(append([]byte{0x45, 0x44}, keynum...), sig...)
	sigText := "untrusted comment: mismatch\n" + base64.StdEncoding.EncodeToString(block)
	err := VerifyMinisign(pubText, sigText, msg)
	if err == nil || !strings.Contains(err.Error(), "keynum") {
		t.Fatalf("keynum 不匹配应报错，got: %v", err)
	}
}

// TestEmbeddedPublicKeyParses 确保填入 update.go 的真实公钥能被正确解码与解析。
// 这是配置正确性的护栏：公钥填错（非法 base64 / 格式不对）会在此暴露。
func TestEmbeddedPublicKeyParses(t *testing.T) {
	pubText, err := signingPublicKey()
	if err != nil {
		t.Fatalf("解码嵌入公钥失败（update.go 的 minisignPublicKeyB64 是否已正确填入）: %v", err)
	}
	pk, keynum, err := parsePublicKey(pubText)
	if err != nil {
		t.Fatalf("解析嵌入公钥失败: %v", err)
	}
	if len(pk) != ed25519PubLen || len(keynum) != keynumLen {
		t.Fatalf("嵌入公钥维度异常: pk=%d keynum=%d", len(pk), len(keynum))
	}
}
