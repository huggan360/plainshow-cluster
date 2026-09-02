// Package identity owns the durable cryptographic identity of one installation.
// A device keeps the same Ed25519 key across every cluster network it joins.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

type Device struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
	ID      string
}

func LoadOrCreate(path string) (*Device, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		return parse(raw)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PLAINSHOW CLUSTER DEVICE KEY", Bytes: encoded})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, block, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return fromPrivate(private), nil
}

func parse(raw []byte) (*Device, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("device key is not PEM encoded")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse device key: %w", err)
	}
	private, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("device key is not Ed25519")
	}
	return fromPrivate(private), nil
}

func fromPrivate(private ed25519.PrivateKey) *Device {
	public := private.Public().(ed25519.PublicKey)
	digest := sha256.Sum256(public)
	return &Device{Private: private, Public: public, ID: hex.EncodeToString(digest[:16])}
}

func (d *Device) Sign(message []byte) []byte { return ed25519.Sign(d.Private, message) }

func Verify(public, message, signature []byte) bool {
	return ed25519.Verify(ed25519.PublicKey(public), message, signature)
}

// TLSCertificate returns a stable self-signed certificate for the device. Mesh
// clients pin its SHA-256 fingerprint from the join code instead of relying on
// a public certificate authority or a DNS name.
func TLSCertificate(device *Device, certPath string) (tls.Certificate, string, error) {
	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, os.ErrNotExist) {
		serialBytes := make([]byte, 8)
		if _, err := rand.Read(serialBytes); err != nil {
			return tls.Certificate{}, "", err
		}
		template := x509.Certificate{
			SerialNumber:          new(big.Int).SetUint64(binary.BigEndian.Uint64(serialBytes)),
			Subject:               pkix.Name{CommonName: "pscluster-" + device.ID},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().AddDate(10, 0, 0),
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
		}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, device.Public, device.Private)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		tmp := certPath + ".tmp"
		if err := os.WriteFile(tmp, certPEM, 0o644); err != nil {
			return tls.Certificate{}, "", err
		}
		if err := os.Rename(tmp, certPath); err != nil {
			_ = os.Remove(tmp)
			return tls.Certificate{}, "", err
		}
	} else if err != nil {
		return tls.Certificate{}, "", err
	}
	// crypto/tls intentionally only accepts conventional PEM labels. The
	// on-disk label is product-specific so diagnostics identify the key, hence
	// re-encode the already parsed key for tls rather than weakening the durable
	// file format or duplicating secret material on disk.
	encodedKey, err := x509.MarshalPKCS8PrivateKey(device.Private)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, "", errors.New("device certificate has no leaf")
	}
	digest := sha256.Sum256(certificate.Certificate[0])
	return certificate, base64.RawURLEncoding.EncodeToString(digest[:]), nil
}
