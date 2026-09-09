package installer

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestManagedBlockRemovesOnlyKnownLegacyPrefix(t *testing.T) {
	id := "test-legacy"
	prefix := "previous generated instructions"
	legacyInstructionPrefixHashes[id] = map[string]bool{fmt.Sprintf("%x", sha256.Sum256([]byte(prefix))): true}
	defer delete(legacyInstructionPrefixHashes, id)

	existing := []byte(prefix + "\n\n<!-- codex-setup:" + id + " -->\nold\n<!-- /codex-setup:" + id + " -->\n")
	got, err := managedBlock(existing, []byte("new"), id)
	if err != nil {
		t.Fatal(err)
	}
	want := "<!-- codex-setup:" + id + " -->\nnew\n<!-- /codex-setup:" + id + " -->\n"
	if string(got) != want {
		t.Fatalf("legacy prefix was not removed safely:\n%s", got)
	}
}
