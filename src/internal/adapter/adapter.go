package adapter

import "context"

type Adapter interface {
	Name() string
	Version() string
	Resolve(name string) (string, error)
	// Preprocess 修改命令参数。
	// 硬性规定：tool == "novaai_shell" 或 "novaai_script" 时，
	// 必须原样返回 args，不得修改用户命令。
	Preprocess(tool, name string, args []string) []string
	Postprocess(tool, name string, stdout []byte) ([]byte, error)
	Verify(ctx context.Context) error
}

type Attempt struct {
	Command string
	Args    []string
	Note    string
}

type FallbackChain []Attempt
