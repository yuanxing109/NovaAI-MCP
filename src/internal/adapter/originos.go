package adapter

type OriginOSAdapter struct{ AOSPAdapter }

func NewOriginOSAdapter(props map[string]string) *OriginOSAdapter {
	if props == nil {
		props = map[string]string{}
	}
	return &OriginOSAdapter{AOSPAdapter{props: props}}
}

func (a *OriginOSAdapter) Name() string { return "originos" }

func (a *OriginOSAdapter) Preprocess(tool, name string, args []string) []string {
	if tool == "novaai_shell" || tool == "novaai_script" {
		return args
	}
	return args
}
