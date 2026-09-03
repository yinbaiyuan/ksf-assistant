package service

import "codexusagebar/core/internal/domain"

func resolveProjectLaunchActions(projects []domain.Project) map[string]domain.ProjectLaunchAction {
	result := map[string]domain.ProjectLaunchAction{}
	for _, project := range projects {
		if action, ok := resolveProjectLaunchAction(project); ok {
			result[project.ID] = action
		}
	}
	return result
}
