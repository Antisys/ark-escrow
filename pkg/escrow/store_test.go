package escrow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, "deals"))
	require.NoError(t, err)
	return store
}

func TestFileStoreSaveAndLoad(t *testing.T) {
	store := newTestStore(t)
	deal := newTestDeal(t)

	err := store.Save(deal)
	require.NoError(t, err)

	loaded, err := store.Load(deal.ID)
	require.NoError(t, err)
	require.Equal(t, deal.ID, loaded.ID)
	require.Equal(t, deal.State, loaded.State)
	require.Equal(t, deal.Title, loaded.Title)
	require.Equal(t, deal.Amount, loaded.Amount)
	require.Equal(t, deal.SellerPubKey, loaded.SellerPubKey)
}

func TestFileStoreLoadNonexistent(t *testing.T) {
	store := newTestStore(t)

	_, err := store.Load("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestFileStoreList(t *testing.T) {
	store := newTestStore(t)

	// Empty store
	deals, err := store.List()
	require.NoError(t, err)
	require.Len(t, deals, 0)

	// Add deals
	deal1 := newTestDeal(t)
	deal2 := newTestDeal(t)
	require.NoError(t, store.Save(deal1))
	require.NoError(t, store.Save(deal2))

	deals, err = store.List()
	require.NoError(t, err)
	require.Len(t, deals, 2)
}

func TestFileStoreOverwrite(t *testing.T) {
	store := newTestStore(t)
	deal := newTestDeal(t)

	require.NoError(t, store.Save(deal))
	deal.State = DealStateFunded
	require.NoError(t, store.Save(deal))

	loaded, err := store.Load(deal.ID)
	require.NoError(t, err)
	require.Equal(t, DealStateFunded, loaded.State)
}

func TestFileStorePermissions(t *testing.T) {
	store := newTestStore(t)
	deal := newTestDeal(t)
	require.NoError(t, store.Save(deal))

	p, err := store.path(deal.ID)
	require.NoError(t, err)
	info, err := os.Stat(p)
	require.NoError(t, err)
	// File should be owner-only readable (0600)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestFileStoreCreateDirectory(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")

	store, err := NewFileStore(nested)
	require.NoError(t, err)
	require.NotNil(t, store)

	info, err := os.Stat(nested)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}
