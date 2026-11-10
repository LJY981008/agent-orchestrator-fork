package ladologwatch

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// certsTestCA is a CA made inside the test: no key file lives in the
// repository.
type certsTestCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func certsTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newCertsTestCA(t *testing.T, name string) certsTestCA {
	t.Helper()
	key := certsTestKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certsTestCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs a leaf certificate; server ones are for 127.0.0.1.
func (ca certsTestCA) issue(t *testing.T, server bool) (certPEM, keyPEM []byte, pair tls.Certificate) {
	t.Helper()
	key := certsTestKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certPEM, keyPEM, pair
}

func certsTestWrite(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func certsTestMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestCertStoreImportStoresProtectedCopies(t *testing.T) {
	src := t.TempDir()
	state := t.TempDir()
	ca := newCertsTestCA(t, "ca")
	certPEM, keyPEM, _ := ca.issue(t, false)
	caPath := certsTestWrite(t, src, "ca.crt", ca.pem)
	certPath := certsTestWrite(t, src, "client.crt", certPEM)
	keyPath := certsTestWrite(t, src, "client.key", keyPEM)

	store := NewCertStore(state)
	name := CertName("proj", "src1")
	if len(name) != 16 || name == CertName("proj", "src2") || name == CertName("proj2", "src1") {
		t.Fatalf("CertName = %q, want 16 hex digits unique per project and source", name)
	}
	if ca, cert, key := store.Stored(name); ca || cert || key {
		t.Fatal("an empty store reports stored files")
	}
	if _, _, _, ok := store.Paths(name); ok {
		t.Fatal("an empty store gives paths")
	}
	if err := store.Import(name, caPath, certPath, keyPath); err != nil {
		t.Fatalf("Import: %v", err)
	}
	dir := filepath.Join(state, "lado", "credentials", "docker", name)
	for _, d := range []string{filepath.Join(state, "lado", "credentials"), filepath.Join(state, "lado", "credentials", "docker"), dir} {
		if mode := certsTestMode(t, d); mode != 0o700 {
			t.Errorf("%s mode = %o, want 700", d, mode)
		}
	}
	for file, want := range map[string][]byte{"ca.pem": ca.pem, "cert.pem": certPEM, "key.pem": keyPEM} {
		path := filepath.Join(dir, file)
		if mode := certsTestMode(t, path); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", file, mode)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s holds something else (%v)", file, err)
		}
	}
	if hasCA, hasCert, hasKey := store.Stored(name); !hasCA || !hasCert || !hasKey {
		t.Fatalf("Stored = %v %v %v", hasCA, hasCert, hasKey)
	}
	gotCA, gotCert, gotKey, ok := store.Paths(name)
	if !ok || gotCA != filepath.Join(dir, "ca.pem") || gotCert != filepath.Join(dir, "cert.pem") || gotKey != filepath.Join(dir, "key.pem") {
		t.Fatalf("Paths = %q %q %q %v", gotCA, gotCert, gotKey, ok)
	}

	// A CA-only import replaces the whole set.
	if err := store.Import(name, caPath, "", ""); err != nil {
		t.Fatalf("Import CA only: %v", err)
	}
	if hasCA, hasCert, hasKey := store.Stored(name); !hasCA || hasCert || hasKey {
		t.Fatalf("after a CA-only import Stored = %v %v %v", hasCA, hasCert, hasKey)
	}
	if _, cert, key, ok := store.Paths(name); !ok || cert != "" || key != "" {
		t.Fatalf("after a CA-only import Paths gives a client certificate")
	}
	entries, _ := os.ReadDir(filepath.Join(state, "lado", "credentials", "docker"))
	if len(entries) != 1 {
		t.Fatalf("docker credentials directory holds %d entries, want only the set", len(entries))
	}

	if err := store.Delete(name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if hasCA, _, _ := store.Stored(name); hasCA {
		t.Fatal("Delete left the CA")
	}
	if err := store.Delete(name); err != nil {
		t.Fatalf("Delete of a missing set: %v", err)
	}
	// The originals are untouched.
	if got, _ := os.ReadFile(keyPath); !bytes.Equal(got, keyPEM) {
		t.Fatal("Import or Delete changed the original key")
	}
}

func TestCertStoreImportRefusals(t *testing.T) {
	src := t.TempDir()
	state := t.TempDir()
	ca := newCertsTestCA(t, "ca")
	certPEM, keyPEM, _ := ca.issue(t, false)
	_, otherKey, _ := ca.issue(t, false)

	caPath := certsTestWrite(t, src, "ca.crt", ca.pem)
	certPath := certsTestWrite(t, src, "client.crt", certPEM)
	keyPath := certsTestWrite(t, src, "client.key", keyPEM)
	otherKeyPath := certsTestWrite(t, src, "other.key", otherKey)
	textPath := certsTestWrite(t, src, "notes.txt", []byte("not a certificate\n"))
	caWithKey := certsTestWrite(t, src, "mixed.crt", append(append([]byte{}, ca.pem...), keyPEM...))
	badDER := certsTestWrite(t, src, "bad.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("junk")}))
	big := certsTestWrite(t, src, "big.crt", append(bytes.Repeat([]byte("#"), certsMaxFile), ca.pem...))
	encLegacy := certsTestWrite(t, src, "enc-legacy.key", pem.EncodeToMemory(&pem.Block{
		Type:    "EC PRIVATE KEY",
		Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-256-CBC,00112233445566778899AABBCCDDEEFF"},
		Bytes:   []byte("sealed"),
	}))
	encPKCS8 := certsTestWrite(t, src, "enc-pkcs8.key", pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("sealed")}))
	missing := filepath.Join(src, "missing.crt")

	store := NewCertStore(state)
	name := CertName("proj", "src")
	cases := []struct {
		name          string
		set           string
		ca, cert, key string
		want          error
	}{
		{name: "no CA", set: name, cert: certPath, key: keyPath, want: ErrCertInvalid},
		{name: "CA not PEM", set: name, ca: textPath, want: ErrCertInvalid},
		{name: "CA with a key block", set: name, ca: caWithKey, want: ErrCertInvalid},
		{name: "CA does not parse", set: name, ca: badDER, want: ErrCertInvalid},
		{name: "CA too large", set: name, ca: big, want: ErrCertInvalid},
		{name: "CA missing", set: name, ca: missing, want: ErrCertInvalid},
		{name: "CA is a directory", set: name, ca: src, want: ErrCertInvalid},
		{name: "cert without key", set: name, ca: caPath, cert: certPath, want: ErrCertInvalid},
		{name: "key without cert", set: name, ca: caPath, key: keyPath, want: ErrCertInvalid},
		{name: "cert and key do not pair", set: name, ca: caPath, cert: certPath, key: otherKeyPath, want: ErrCertInvalid},
		{name: "key not PEM", set: name, ca: caPath, cert: certPath, key: textPath, want: ErrCertInvalid},
		{name: "cert not PEM", set: name, ca: caPath, cert: textPath, key: keyPath, want: ErrCertInvalid},
		{name: "legacy encrypted key", set: name, ca: caPath, cert: certPath, key: encLegacy, want: ErrCertKeyEncrypted},
		{name: "PKCS#8 encrypted key", set: name, ca: caPath, cert: certPath, key: encPKCS8, want: ErrCertKeyEncrypted},
		{name: "bad set name", set: "../escape", ca: caPath, want: ErrCertInvalid},
		{name: "empty set name", set: "", ca: caPath, want: ErrCertInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := store.Import(tc.set, tc.ca, tc.cert, tc.key)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Import = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), src) || strings.Contains(err.Error(), state) {
				t.Fatalf("error %q names a path", err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(state, "lado")); !os.IsNotExist(err) {
		t.Fatal("a refused import wrote to the state directory")
	}

	// A refused import leaves a stored set as it was.
	if err := store.Import(name, caPath, certPath, keyPath); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if err := store.Import(name, caPath, certPath, otherKeyPath); !errors.Is(err, ErrCertInvalid) {
		t.Fatalf("Import of a mismatched pair = %v", err)
	}
	_, _, key, ok := store.Paths(name)
	if got, _ := os.ReadFile(key); !ok || !bytes.Equal(got, keyPEM) {
		t.Fatal("a refused import changed the stored set")
	}
}

// certsTestTLSEngine starts a fake Engine over TLS with the given server
// certificate; with clientCA it needs a client certificate from that CA.
func certsTestTLSEngine(t *testing.T, server tls.Certificate, clientCA *x509.Certificate) (*dockerTestEngine, string) {
	t.Helper()
	engine := &dockerTestEngine{gone: map[string]bool{}, redirect: map[string]string{}}
	engine.containers = []dockerTestContainer{dockerTestCompose("tls1", "grp-api-1", "grp", "api")}
	srv := httptest.NewUnstartedServer(engine)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{server}, MinVersion: tls.VersionTLS12}
	if clientCA != nil {
		pool := x509.NewCertPool()
		pool.AddCert(clientCA)
		srv.TLS.ClientCAs = pool
		srv.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	engine.srv = srv
	u, _ := url.Parse(srv.URL)
	return engine, "tcp://" + u.Host
}

func TestDockerTLSWithStoredCertificates(t *testing.T) {
	src := t.TempDir()
	ca := newCertsTestCA(t, "ca")
	_, _, serverPair := ca.issue(t, true)
	clientCert, clientKey, _ := ca.issue(t, false)
	engine, endpoint := certsTestTLSEngine(t, serverPair, ca.cert)

	store := NewCertStore(t.TempDir())
	name := CertName("proj", "tls")
	if err := store.Import(name, certsTestWrite(t, src, "ca.crt", ca.pem), certsTestWrite(t, src, "c.crt", clientCert), certsTestWrite(t, src, "c.key", clientKey)); err != nil {
		t.Fatalf("Import: %v", err)
	}
	caFile, certFile, keyFile, ok := store.Paths(name)
	if !ok {
		t.Fatal("no stored set")
	}
	cfg := DockerConfig{Endpoint: endpoint, TLS: true, CAFile: caFile, CertFile: certFile, KeyFile: keyFile,
		Targets: []DockerTarget{{ID: "t", Kind: dockerTargetComposeProject, Value: "grp"}}}
	ctx := context.Background()
	check, err := CheckDocker(ctx, cfg)
	if err != nil {
		t.Fatalf("CheckDocker over TLS: %v", err)
	}
	if check.Insecure || check.APIVersion != "1.47" || len(check.Instances) != 1 || check.Instances[0].Service != "grp/api" {
		t.Fatalf("check = %+v", check)
	}

	// Without the client certificate the Engine refuses the handshake.
	noClient := cfg
	noClient.CertFile, noClient.KeyFile = "", ""
	if _, err := CheckDocker(ctx, noClient); err == nil {
		t.Fatal("CheckDocker without a client certificate succeeded")
	}
	// A CA that did not sign the server is refused.
	other := newCertsTestCA(t, "other")
	otherCA := certsTestWrite(t, src, "other.crt", other.pem)
	wrongCA := cfg
	wrongCA.CAFile = otherCA
	if _, err := CheckDocker(ctx, wrongCA); err == nil {
		t.Fatal("CheckDocker with the wrong CA succeeded")
	}
	// Plain http to a TLS Engine fails too.
	plain := cfg
	plain.TLS = false
	if _, err := CheckDocker(ctx, plain); err == nil {
		t.Fatal("CheckDocker over plain http to a TLS engine succeeded")
	}
	for _, r := range engine.recorded() {
		if r.Method != http.MethodGet {
			t.Fatalf("engine received %s", r.Method)
		}
	}
}

func TestDockerTLSWithHTTPTestServer(t *testing.T) {
	engine := &dockerTestEngine{gone: map[string]bool{}, redirect: map[string]string{}}
	srv := httptest.NewTLSServer(engine)
	t.Cleanup(srv.Close)
	engine.srv = srv
	caFile := certsTestWrite(t, t.TempDir(), "ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	u, _ := url.Parse(srv.URL)
	check, err := CheckDocker(context.Background(), DockerConfig{Endpoint: "tcp://" + u.Host, TLS: true, CAFile: caFile})
	if err != nil {
		t.Fatalf("CheckDocker: %v", err)
	}
	if check.Insecure || check.EngineVersion != "27.0.1" {
		t.Fatalf("check = %+v", check)
	}
}

func TestDockerTLSConfig(t *testing.T) {
	src := t.TempDir()
	ca := newCertsTestCA(t, "ca")
	certPEM, keyPEM, _ := ca.issue(t, false)
	caFile := certsTestWrite(t, src, "ca.pem", ca.pem)
	certFile := certsTestWrite(t, src, "cert.pem", certPEM)
	keyFile := certsTestWrite(t, src, "key.pem", keyPEM)

	cfg, err := dockerTLSConfig(caFile, certFile, keyFile, "engine.example.test")
	if err != nil {
		t.Fatalf("dockerTLSConfig: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS12 || cfg.ServerName != "engine.example.test" || cfg.RootCAs == nil || len(cfg.Certificates) != 1 || cfg.InsecureSkipVerify {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg, err = dockerTLSConfig(caFile, "", "", "192.0.2.10"); err != nil || len(cfg.Certificates) != 0 {
		t.Fatalf("CA only = %+v, %v", cfg, err)
	}
	if _, err := dockerTLSConfig("", "", "", "x"); err == nil {
		t.Fatal("no CA succeeded")
	}
	if _, err := dockerTLSConfig(caFile, certFile, "", "x"); err == nil {
		t.Fatal("cert without key succeeded")
	}
	if _, err := dockerTLSConfig(certsTestWrite(t, src, "bad.pem", []byte("x")), "", "", "x"); err == nil {
		t.Fatal("bad CA succeeded")
	}
	// NewDockerSource verifies against the endpoint's host, brackets off.
	s, err := NewDockerSource(DockerConfig{Endpoint: "tcp://[2001:db8::1]:2376", TLS: true, CAFile: caFile})
	if err != nil {
		t.Fatalf("NewDockerSource: %v", err)
	}
	if s.base.Scheme != "https" {
		t.Fatalf("scheme = %q, want https", s.base.Scheme)
	}
	tr := s.client.Transport.(*dockerReadOnlyTransport).base.(*http.Transport)
	if tr.TLSClientConfig.ServerName != "2001:db8::1" {
		t.Fatalf("ServerName = %q", tr.TLSClientConfig.ServerName)
	}
}
