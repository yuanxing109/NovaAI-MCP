package config

// DefaultProfileName 是唯一的档位名。
//
// 重构后不再有 conservative / readonly / reverse / agent_full：
// 单用户自有设备，装完即用、含 shell 是明确诉求；权限边界落在
// **网络可达性**上，而不是档位。详见 docs/security.md。
const DefaultProfileName = "default"

// DefaultProfiles 是内置档位的唯一定义处。
//
// 只有 default：allowTools 为通配、denyTools 为空、riskCeiling 3（最高）。
// shell 是万能绕过，这里放行它是有意的 —— 见 docs/KNOWN_ISSUES.md
// 的残余风险第 1 条与第 4 条。
func DefaultProfiles() map[string]Profile {
	return map[string]Profile{
		DefaultProfileName: {
			AllowTools:  []string{"*"},
			DenyTools:   []string{},
			RiskCeiling: 3,
		},
	}
}
