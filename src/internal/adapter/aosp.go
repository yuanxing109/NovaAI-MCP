package adapter

import (
	"context"
	"os/exec"
)

type AOSPAdapter struct {
	props map[string]string
}

func NewAOSPAdapter(props map[string]string) *AOSPAdapter {
	if props == nil {
		props = map[string]string{}
	}
	return &AOSPAdapter{props: props}
}

func (a *AOSPAdapter) Name() string    { return "aosp" }
func (a *AOSPAdapter) Version() string { return a.props["ro.build.version.release"] }

func (a *AOSPAdapter) Resolve(name string) (string, error) {
	return exec.LookPath(name)
}

func (a *AOSPAdapter) Preprocess(tool, name string, args []string) []string {
	if tool == "novaai_shell" || tool == "novaai_script" {
		return args
	}
	return args
}

func (a *AOSPAdapter) Postprocess(tool, name string, stdout []byte) ([]byte, error) {
	return stdout, nil
}

func (a *AOSPAdapter) Verify(ctx context.Context) error {
	return nil
}
