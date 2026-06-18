package monitor

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// minisign 签名校验 —— 对齐 CC Switch（Tauri updater）的做法：发布方用私钥签名
// 安装包，应用内嵌公钥，下载后做端到端校验，防止 CDN/中间人篡改。
//
// minisign 文本格式（公钥 .pub 与签名 .sig 各两行）：
//
//	untrusted comment: ...
//	<base64>
//
// base64 解码后的二进制布局：
//   - 公钥：[sig_alg(2)][keynum(8)][pk(32)]  共 42 字节
//   - 签名：[sig_alg(2)][keynum(8)][sig(64)] 共 74 字节
//
// sig_alg 两字节决定签名方式：
//   - "Ed" (0x45,0x64)：Ed25519 直接签名（legacy，少用）
//   - "ED" (0x45,0x44)：Ed25519 + blake2b-512 预哈希 —— minisign/Tauri 对大文件默认用此

const (
	keynumLen     = 8
	ed25519PubLen = ed25519.PublicKeySize // 32
	ed25519SigLen = ed25519.SignatureSize // 64
)

var (
	pubAlgEd          = [2]byte{0x45, 0x64} // "Ed" — 公钥固定算法标识
	sigAlgEdPure      = [2]byte{0x45, 0x64} // "Ed" — 直接签名
	sigAlgEdPrehashed = [2]byte{0x45, 0x44} // "ED" — blake2b 预哈希签名
)

// extractB64Line 从 minisign 文本中取出首个 base64 行。
// 公钥文本通常是两行；签名文本既兼容两行简化格式，也兼容标准四行 .minisig。
// 标准 .minisig 的第二个 base64 是对 trusted comment 的签名，不是文件签名本体，
// 校验文件时必须取首个 base64 行。
func extractB64Line(text string) (string, error) {
	for _, raw := range strings.Split(text, "\n") {
		ln := strings.TrimSpace(raw)
		if ln == "" || strings.HasPrefix(ln, "untrusted comment:") || strings.HasPrefix(ln, "trusted comment:") {
			continue
		}
		return ln, nil
	}
	return "", errors.New("minisign: 文本中缺少 base64 行")
}

// VerifyMinisign 用 minisign 公钥文本校验 message 的签名文本。
// pubKeyText 为 minisign 两行公钥文本；signatureText 兼容两行签名文本与标准四行 .minisig 文本。
// message 为被签名的原始字节（对安装包即整个文件内容）。
func VerifyMinisign(pubKeyText, signatureText string, message []byte) error {
	pk, keynum, err := parsePublicKey(pubKeyText)
	if err != nil {
		return err
	}
	sigAlg, sigKeynum, sig, err := parseSignature(signatureText)
	if err != nil {
		return err
	}
	if !bytes.Equal(keynum, sigKeynum) {
		return errors.New("minisign: 公钥与签名的 keynum 不匹配")
	}

	switch sigAlg {
	case sigAlgEdPrehashed: // "ED" — 先 blake2b-512 再 Ed25519 验证
		digest := blake2b.Sum512(message)
		if !ed25519.Verify(ed25519.PublicKey(pk), digest[:], sig) {
			return errors.New("minisign: 签名校验失败（文件可能已被篡改）")
		}
		return nil
	case sigAlgEdPure: // "Ed" — 直接对原文 Ed25519 验证（legacy 兼容）
		if !ed25519.Verify(ed25519.PublicKey(pk), message, sig) {
			return errors.New("minisign: 签名校验失败（文件可能已被篡改）")
		}
		return nil
	default:
		return fmt.Errorf("minisign: 不支持的签名算法 0x%02x%02x", sigAlg[0], sigAlg[1])
	}
}

// parsePublicKey 解析公钥文本 → (pk, keynum)。
func parsePublicKey(text string) (pk, keynum []byte, err error) {
	b64, err := extractB64Line(text)
	if err != nil {
		return nil, nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, nil, fmt.Errorf("minisign: 公钥 base64 解码失败: %w", err)
	}
	if len(raw) != 2+keynumLen+ed25519PubLen {
		return nil, nil, fmt.Errorf("minisign: 公钥长度异常 (%d 字节)", len(raw))
	}
	alg := [2]byte{raw[0], raw[1]}
	if alg != pubAlgEd {
		return nil, nil, fmt.Errorf("minisign: 公钥算法标识异常 0x%02x%02x", alg[0], alg[1])
	}
	keynum = raw[2 : 2+keynumLen]
	pk = raw[2+keynumLen:]
	return pk, keynum, nil
}

// parseSignature 解析签名文本 → (sigAlg, keynum, sig)。
func parseSignature(text string) (alg [2]byte, keynum, sig []byte, err error) {
	b64, err := extractB64Line(text)
	if err != nil {
		return [2]byte{}, nil, nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return [2]byte{}, nil, nil, fmt.Errorf("minisign: 签名 base64 解码失败: %w", err)
	}
	if len(raw) != 2+keynumLen+ed25519SigLen {
		return [2]byte{}, nil, nil, fmt.Errorf("minisign: 签名长度异常 (%d 字节)", len(raw))
	}
	alg = [2]byte{raw[0], raw[1]}
	keynum = raw[2 : 2+keynumLen]
	sig = raw[2+keynumLen:]
	return alg, keynum, sig, nil
}
