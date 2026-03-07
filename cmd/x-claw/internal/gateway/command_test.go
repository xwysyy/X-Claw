package gateway

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGatewayCommand(t *testing.T) {
	cmd := NewGatewayCommand()

	require.NotNil(t, cmd)

	assert.Equal(t, "gateway", cmd.Use)
	assert.Equal(t, "Start X-Claw gateway", cmd.Short)

	assert.Len(t, cmd.Aliases, 1)
	assert.True(t, cmd.HasAlias("g"))

	assert.Nil(t, cmd.Run)
	assert.NotNil(t, cmd.RunE)

	assert.Nil(t, cmd.PersistentPreRun)
	assert.Nil(t, cmd.PersistentPostRun)

	assert.False(t, cmd.HasSubCommands())

	assert.True(t, cmd.HasFlags())
	assert.NotNil(t, cmd.Flags().Lookup("debug"))
}

func TestNewGatewayCommand_UsesGatewayRuntimeRunner(t *testing.T) {
	originalRunner := gatewayCommandRunner
	t.Cleanup(func() {
		gatewayCommandRunner = originalRunner
	})

	called := false
	gotDebug := false
	gatewayCommandRunner = func(debug bool) error {
		called = true
		gotDebug = debug
		return nil
	}

	cmd := NewGatewayCommand()
	cmd.SetArgs([]string{"--debug"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.True(t, called)
	assert.True(t, gotDebug)
}

func TestNewGatewayCommand_DefaultRunnerUsesFullGatewayRuntime(t *testing.T) {
	fn := runtime.FuncForPC(reflect.ValueOf(gatewayCommandRunner).Pointer())
	require.NotNil(t, fn)
	assert.Contains(t, fn.Name(), ".gatewayCmd")
}

func TestInternalAppPackageRemoved(t *testing.T) {
	deadPath := filepath.Join("..", "..", "..", "..", "internal", "app", "app.go")
	_, err := os.Stat(deadPath)
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err), "internal/app/app.go should be removed, got err=%v", err)
}
