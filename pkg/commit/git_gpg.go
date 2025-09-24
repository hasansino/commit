package commit

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ProtonMail/go-crypto/openpgp"
	"golang.org/x/term"
)

// gpgSigner implements the go-git Signer interface using gpg command
type gpgSigner struct {
	gpgProgram string
	keyID      string
}

func (g *gpgSigner) Sign(message io.Reader) ([]byte, error) {
	// Read the message to be signed
	messageBytes, err := io.ReadAll(message)
	if err != nil {
		return nil, fmt.Errorf("failed to read message: %w", err)
	}

	// Use gpg command to sign the message, leveraging gpg-agent
	cmd := exec.Command(g.gpgProgram, "--detach-sign", "--armor", "--local-user", g.keyID)
	cmd.Stdin = strings.NewReader(string(messageBytes))

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gpg signing failed: %w", err)
	}

	return output, nil
}

// isGPGAgentAvailable checks if gpg-agent is running
func (g *gitOperations) isGPGAgentAvailable(gpgProgram string) bool {
	// Check if GPG_AGENT_INFO is set (older GPG versions)
	if os.Getenv("GPG_AGENT_INFO") != "" {
		return true
	}

	// For newer GPG versions, try to connect to the agent
	cmd := exec.Command(gpgProgram, "--batch", "--list-secret-keys")
	err := cmd.Run()
	return err == nil
}

// createGPGSigner creates a GPG signer that uses gpg-agent's cached credentials
func (g *gitOperations) createGPGSigner(config *gitConfig) (*gpgSigner, error) {
	// Verify that the key exists and is available
	cmd := exec.Command(config.GPGProgram, "--list-secret-keys", config.SigningKey)
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("signing key %s not found or not available", config.SigningKey)
	}

	return &gpgSigner{
		gpgProgram: config.GPGProgram,
		keyID:      config.SigningKey,
	}, nil
}

// loadKeyDirectly loads key directly from keyring (fallback method)
func (g *gitOperations) loadKeyDirectly(config *gitConfig) (*openpgp.Entity, error) {
	// Try to get GPG key from user's keyring
	keyring, err := g.getGPGKeyring(config.GPGProgram)
	if err != nil {
		return nil, fmt.Errorf("failed to access GPG keyring: %w", err)
	}

	// Find the specific signing key
	for _, entity := range keyring {
		// Check if this entity matches the signing key ID
		if g.matchesSigningKey(entity, config.SigningKey) {
			// Check if the key is encrypted and needs to be decrypted
			if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
				err := g.decryptPrivateKey(entity, config.SigningKey)
				if err != nil {
					return nil, fmt.Errorf("failed to decrypt signing key %s: %w", config.SigningKey, err)
				}
			}
			return entity, nil
		}
	}

	return nil, fmt.Errorf("signing key %s not found in keyring", config.SigningKey)
}

// getGPGKeyring reads the user's GPG keyring
func (g *gitOperations) getGPGKeyring(gpgProgram string) (openpgp.EntityList, error) {
	// Get GPG home directory
	gpgHome := os.Getenv("GNUPGHOME")
	if gpgHome == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		gpgHome = filepath.Join(homeDir, ".gnupg")
	}

	// Try to read secret keyring
	secretKeyringPath := filepath.Join(gpgHome, "secring.gpg")
	if _, err := os.Stat(secretKeyringPath); err == nil {
		// Old GPG format
		file, err := os.Open(secretKeyringPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open secret keyring: %w", err)
		}
		defer file.Close()
		return openpgp.ReadArmoredKeyRing(file)
	}

	// For newer GPG versions, we need to export keys
	return g.exportGPGKeys(gpgProgram)
}

// exportGPGKeys exports GPG keys using the gpg command
func (g *gitOperations) exportGPGKeys(gpgProgram string) (openpgp.EntityList, error) {
	// Export secret keys in ASCII armor format
	cmd := exec.Command(gpgProgram, "--export-secret-keys", "--armor")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to export GPG keys: %w", err)
	}

	// Parse the exported keys
	return openpgp.ReadArmoredKeyRing(strings.NewReader(string(output)))
}

// matchesSigningKey checks if a GPG entity matches the signing key identifier
func (g *gitOperations) matchesSigningKey(entity *openpgp.Entity, signingKey string) bool {
	// Check primary key ID (full or short form)
	primaryKeyID := fmt.Sprintf("%016X", entity.PrimaryKey.KeyId)
	if strings.HasSuffix(primaryKeyID, strings.ToUpper(signingKey)) {
		return true
	}

	// Check subkey IDs
	for _, subkey := range entity.Subkeys {
		subkeyID := fmt.Sprintf("%016X", subkey.PublicKey.KeyId)
		if strings.HasSuffix(subkeyID, strings.ToUpper(signingKey)) {
			return true
		}
	}

	// Check user IDs (email addresses)
	for _, identity := range entity.Identities {
		if strings.Contains(identity.UserId.Email, signingKey) {
			return true
		}
	}

	return false
}

// decryptPrivateKey prompts for passphrase and decrypts the GPG private key
func (g *gitOperations) decryptPrivateKey(entity *openpgp.Entity, keyID string) error {
	fmt.Printf("Enter passphrase for GPG key %s: ", keyID)

	// Read passphrase securely (without echoing to terminal)
	passphrase, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return fmt.Errorf("failed to read passphrase: %w", err)
	}
	fmt.Println() // Add newline after password input

	// Attempt to decrypt the private key with the passphrase
	err = entity.PrivateKey.Decrypt(passphrase)
	if err != nil {
		return fmt.Errorf("incorrect passphrase or decryption failed: %w", err)
	}

	// Also decrypt subkeys if they exist
	for _, subkey := range entity.Subkeys {
		if subkey.PrivateKey != nil && subkey.PrivateKey.Encrypted {
			err = subkey.PrivateKey.Decrypt(passphrase)
			if err != nil {
				return fmt.Errorf("failed to decrypt subkey: %w", err)
			}
		}
	}

	return nil
}
