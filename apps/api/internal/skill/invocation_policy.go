package skill

import (
	"bytes"
	"io"
	"os"

	"agentflow-platform/apps/api/internal/domain"
	"gopkg.in/yaml.v3"
)

// Only these restrictive declarations are implemented. Unknown behavior is
// diagnosed by Checker; a malformed invocation restriction fails closed here.
func packageInvocationPolicy(root *os.Root, doc document) (domain.SkillInvocationPolicy, error) {
	value := domain.SkillInvocationPolicy("")
	if node, ok := doc.Fields["disable-model-invocation"]; ok {
		disabled, err := strictBoolean(node)
		if err != nil {
			return "", err
		}
		if disabled {
			value = domain.SkillExplicitOnly
		}
	}
	if _, ok := doc.Fields["policy"]; ok {
		return "", skillError("skill_invocation_invalid", "Codex invocation policy belongs in agents/openai.yaml, not SKILL.md")
	}
	info, err := root.Lstat("agents")
	if os.IsNotExist(err) {
		return value, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", skillError("skill_invalid_resource", "Skill agents metadata must be a real directory")
	}
	if _, err := root.Lstat("agents/openai.yaml"); os.IsNotExist(err) {
		return value, nil
	}
	data, err := readText(root, "agents/openai.yaml", 4096)
	if err != nil {
		return "", err
	}
	var metadata struct {
		Policy       yaml.Node `yaml:"policy"`
		Dependencies yaml.Node `yaml:"dependencies"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&metadata); err != nil {
		return "", skillError("skill_invocation_invalid", "Invalid agents/openai.yaml metadata")
	}
	if metadata.Dependencies.Kind != 0 {
		return "", skillError("skill_dependency_unsupported", "Codex Tool dependencies are not mapped; declare AgentFlow required Tool names explicitly")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return "", skillError("skill_invocation_invalid", "Multiple agents/openai.yaml documents")
	}
	node := metadata.Policy
	if node.Kind == 0 {
		return value, nil
	}
	if node.Kind != yaml.MappingNode {
		return "", skillError("skill_invocation_invalid", "Skill policy must be a mapping")
	}
	seen := false
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != "allow_implicit_invocation" || seen {
			return "", skillError("skill_invocation_invalid", "Unsupported or duplicate Skill invocation policy")
		}
		seen = true
		allowed, err := strictBoolean(*node.Content[i+1])
		if err != nil {
			return "", err
		}
		if !allowed {
			value = domain.SkillExplicitOnly
		}
	}
	return value, nil
}

func strictBoolean(node yaml.Node) (bool, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" || node.Value != "true" && node.Value != "false" {
		return false, skillError("skill_invocation_invalid", "Skill invocation restrictions require literal true or false")
	}
	return node.Value == "true", nil
}
