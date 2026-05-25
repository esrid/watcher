package watcher

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockDriver struct{}

func (m *mockDriver) Open(name string) (driver.Conn, error) {
	return nil, nil
}

type wrapDriver struct {
	inner driver.Driver
}

func (w *wrapDriver) Open(name string) (driver.Conn, error) {
	return nil, nil
}

func (w *wrapDriver) Unwrap() driver.Driver {
	return w.inner
}

type cycleDriver struct{}

func (c *cycleDriver) Open(name string) (driver.Conn, error) {
	return nil, nil
}

func (c *cycleDriver) Unwrap() driver.Driver {
	return c
}

func TestUnwrapDriver(t *testing.T) {
	raw := &mockDriver{}

	// 1. Raw driver (no Unwrap)
	assert.Equal(t, raw, unwrapDriver(raw))

	// 2. Single wrap
	wrap1 := &wrapDriver{inner: raw}
	assert.Equal(t, raw, unwrapDriver(wrap1))

	// 3. Double wrap
	wrap2 := &wrapDriver{inner: wrap1}
	assert.Equal(t, raw, unwrapDriver(wrap2))

	// 4. Cycle prevention
	cycle := &cycleDriver{}
	assert.Equal(t, cycle, unwrapDriver(cycle))
}
