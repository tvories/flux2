/*
Copyright 2021 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package deploysecret

import (
	"bytes"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net"
	"path"
	"time"

	cryptssh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/fluxcd/pkg/ssh"

	"github.com/fluxcd/flux2/pkg/manifestgen"
)

const defaultSSHPort = 22

func Generate(options Options) (*manifestgen.Manifest, error) {
	var err error

	var keypair *ssh.KeyPair
	switch {
	case options.Username != "", options.Password != "":
		// noop
	case options.PrivateKeyPath != "":
		if keypair, err = loadKeyPair(options.PrivateKeyPath); err != nil {
			return nil, err
		}
	case options.PrivateKeyAlgorithm != "":
		if keypair, err = generateKeyPair(options); err != nil {
			return nil, err
		}
	}

	var hostKey []byte
	if keypair != nil {
		if hostKey, err = scanHostKey(options.SSHHostname); err != nil {
			return nil, err
		}
	}

	var caFile []byte
	if options.CAFilePath != "" {
		if caFile, err = ioutil.ReadFile(options.CAFilePath); err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}
	}

	secret := makeSecret(keypair, hostKey, caFile, options)
	b, err := yaml.Marshal(secret)
	if err != nil {
		return nil, err
	}

	return &manifestgen.Manifest{
		Path:    path.Join(options.TargetPath, options.Namespace, options.ManifestFile),
		Content: fmt.Sprintf("---\n%s", resourceToString(b)),
	}, nil
}

func makeSecret(keypair *ssh.KeyPair, hostKey []byte, caFile []byte, options Options) (secret corev1.Secret) {
	secret.ObjectMeta = metav1.ObjectMeta{
		Name:      options.Name,
		Namespace: options.Namespace,
	}
	secret.StringData = map[string]string{}

	if options.Username != "" || options.Password != "" {
		secret.StringData[UsernameSecretKey] = options.Username
		secret.StringData[PasswordSecretKey] = options.Password
	}

	if caFile != nil {
		secret.StringData[CAFileSecretKey] = string(caFile)
	}

	if keypair != nil && hostKey != nil {
		secret.StringData[PrivateKeySecretKey] = string(keypair.PrivateKey)
		secret.StringData[PublicKeySecretKey] = string(keypair.PublicKey)
		secret.StringData[KnownHostsSecretKey] = string(hostKey)
	}

	return
}

func loadKeyPair(path string) (*ssh.KeyPair, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open private key file: %w", err)
	}

	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	ppk, err := cryptssh.ParsePrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	return &ssh.KeyPair{
		PublicKey:  cryptssh.MarshalAuthorizedKey(ppk.PublicKey()),
		PrivateKey: b,
	}, nil
}

func generateKeyPair(options Options) (*ssh.KeyPair, error) {
	var keyGen ssh.KeyPairGenerator
	switch options.PrivateKeyAlgorithm {
	case RSAPrivateKeyAlgorithm:
		keyGen = ssh.NewRSAGenerator(options.RSAKeyBits)
	case ECDSAPrivateKeyAlgorithm:
		keyGen = ssh.NewECDSAGenerator(options.ECDSACurve)
	case Ed25519PrivateKeyAlgorithm:
		keyGen = ssh.NewEd25519Generator()
	default:
		return nil, fmt.Errorf("unsupported public key algorithm: %s", options.PrivateKeyAlgorithm)
	}
	pair, err := keyGen.Generate()
	if err != nil {
		return nil, fmt.Errorf("key pair generation failed, error: %w", err)
	}
	return pair, nil
}

func scanHostKey(host string) ([]byte, error) {
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = fmt.Sprintf("%s:%d", host, defaultSSHPort)
	}
	hostKey, err := ssh.ScanHostKey(host, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("SSH key scan for host %s failed, error: %w", host, err)
	}
	return hostKey, nil
}

func resourceToString(data []byte) string {
	data = bytes.Replace(data, []byte("  creationTimestamp: null\n"), []byte(""), 1)
	data = bytes.Replace(data, []byte("status: {}\n"), []byte(""), 1)
	return string(data)
}
