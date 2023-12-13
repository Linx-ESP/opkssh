package commands

import (
	"context"
	"crypto"
	"encoding/pem"
	"errors"
	"fmt"
	"freessh/sshcert"
	"os"
	"path/filepath"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/openpubkey/openpubkey/client"
	"github.com/openpubkey/openpubkey/util"
	"golang.org/x/crypto/ssh"
)

func Login(op client.OpenIdProvider) error {
	// If principals is empty the server does not enforce any principal. The OPK
	// verifier should use policy to make this decision.
	principals := []string{}

	gqFalse := false
	alg := jwa.ES256

	signer, err := util.GenKeyPair(alg)
	if err != nil {
		return fmt.Errorf("failed to generate keypair: %w", err)
	}

	client := &client.OpkClient{
		Op: op,
	}
	certBytes, seckeySshPem, err := createSSHCert(context.Background(), client, signer, alg, gqFalse, principals)
	if err != nil {
		return fmt.Errorf("failed to generate SSH cert: %w", err)
	}

	// Write ssh secret key and public key to filesystem
	err = writeKeysToSSHDir(seckeySshPem, certBytes)
	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to write SSH keys to filesystem: %w", err)
	}
	return nil
}

func createSSHCert(cxt context.Context, client *client.OpkClient, signer crypto.Signer, alg jwa.KeyAlgorithm, gqFlag bool, principals []string) ([]byte, []byte, error) {
	pkt, err := client.OidcAuth(cxt, signer, alg, map[string]any{}, gqFlag)
	if err != nil {
		return nil, nil, err
	}
	cert, err := sshcert.New(pkt, principals)
	if err != nil {
		return nil, nil, err
	}
	sshSigner, err := ssh.NewSignerFromSigner(signer)
	if err != nil {
		return nil, nil, err
	}

	signerMas, err := ssh.NewSignerWithAlgorithms(sshSigner.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoECDSA256})
	if err != nil {
		return nil, nil, err
	}

	sshCert, err := cert.SignCert(signerMas)
	if err != nil {
		return nil, nil, err
	}
	certBytes := ssh.MarshalAuthorizedKey(sshCert)
	// Remove newline character that MarshalAuthorizedKey() adds
	certBytes = certBytes[:len(certBytes)-1]

	seckeySsh, err := ssh.MarshalPrivateKey(signer, "openpubkey cert")
	if err != nil {
		return nil, nil, err
	}
	seckeySshBytes := pem.EncodeToMemory(seckeySsh)

	return certBytes, seckeySshBytes, nil
}

func writeKeysToSSHDir(seckeySshPem []byte, certBytes []byte) error {
	homePath, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sshPath := filepath.Join(homePath, ".ssh")

	// For ssh to automatically find the key created by openpubkey when
	// connecting, we use one of the default ssh key paths. However, the file
	// might contain an existing key. We will overwrite the key if it was
	// generated by openpubkey  which we check by looking at the associated
	// comment. If the comment is equal to "openpubkey", we overwrite the file
	// with a new key.
	for _, keyFilename := range []string{"id_ecdsa", "id_dsa"} {
		seckeyPath := filepath.Join(sshPath, keyFilename)
		pubkeyPath := seckeyPath + ".pub"

		if !fileExists(seckeyPath) {
			// If ssh key file does not currently exist, we don't have to worry about overwriting it
			return writeKeys(seckeyPath, pubkeyPath, seckeySshPem, certBytes)
		} else if !fileExists(pubkeyPath) {
			continue
		} else {
			// If the ssh key file does exist, check if it was generated by openpubkey, if it was then it is safe to overwrite
			sshPubkey, err := os.ReadFile(pubkeyPath)
			if err != nil {
				fmt.Println("Failed to read:", pubkeyPath)
				continue
			}
			_, comment, _, _, err := ssh.ParseAuthorizedKey(sshPubkey)
			if err != nil {
				fmt.Println("Failed to parse:", pubkeyPath)
				continue
			}

			// If the key comment is "openpubkey" then we generated it
			if comment == "openpubkey" {
				return writeKeys(seckeyPath, pubkeyPath, seckeySshPem, certBytes)
			}
		}
	}
	return fmt.Errorf("no default ssh key file free for openpubkey")
}

func writeKeys(seckeyPath string, pubkeyPath string, seckeySshPem []byte, certBytes []byte) error {
	// Write ssh secret key to filesystem
	if err := os.WriteFile(seckeyPath, seckeySshPem, 0600); err != nil {
		return err
	}

	fmt.Println("writing secret key to", seckeyPath)
	fmt.Println("writing public key to", pubkeyPath)

	certBytes = append(certBytes, []byte(" openpubkey")...)
	// Write ssh public key (certificate) to filesystem
	return os.WriteFile(pubkeyPath, certBytes, 0777)
}

func fileExists(fPath string) bool {
	_, err := os.Open(fPath)
	return !errors.Is(err, os.ErrNotExist)
}
