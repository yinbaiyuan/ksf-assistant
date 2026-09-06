package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"ksfassistant/core/internal/domain"
	"ksfassistant/core/internal/taskruntime"
)

func (service *Service) enrichTaskRuntime(ctx context.Context, root string, projects *domain.ProjectDashboardSnapshot, activity domain.TaskActivitySnapshot, now time.Time) {
	if root == "" || activity.Availability != "available" || len(projects.Projects) == 0 {
		return
	}
	store, err := taskruntime.Open(root, taskruntime.Options{Now: func() time.Time { return now }})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	applyTaskRuntime(ctx, root, store, projects, activity)
}

func applyTaskRuntime(ctx context.Context, root string, store *taskruntime.Store, dashboard *domain.ProjectDashboardSnapshot, activity domain.TaskActivitySnapshot) {
	if activity.Availability != "available" {
		return
	}
	known := map[string]domain.TaskObservation{}
	for _, observation := range activity.Observations {
		if observation.HostID != "local" || domain.ClassifyTask(observation) == "ignored" {
			continue
		}
		known[observation.HostID+":"+observation.ID] = observation
	}
	groups := map[string]*domain.ProjectDashboardItem{}
	order := []string{}
	for _, group := range dashboard.Projects {
		copy := group
		copy.Tasks = []domain.ProjectTask{}
		groups[group.ID] = &copy
		order = append(order, group.ID)
	}
	readCount := 0
	for _, group := range dashboard.Projects {
		for _, task := range group.Tasks {
			target := group.ID
			if observation, ok := known[task.HostID+":"+task.ThreadID]; ok && readCount < 128 {
				readCount++
				identity, err := store.TaskID(ctx, "codex", task.ThreadID)
				if err == nil {
					view, _, readErr := store.Get(ctx, identity)
					if readErr == nil {
						target = runtimeProjectID(root, dashboard.Catalog, view)
						task.ProjectID = target
						task.TaskRuntime = runtimeState(view, observation, activity.ObservedAt)
						task.Route = nil
						if view.RouteFreshness == "current" {
							receipt, parseErr := taskruntime.ParseReceipt(view.State.Receipt)
							if parseErr == nil {
								task.Route = runtimeRoute(receipt)
							}
						}
					} else if !runtimeErrorIs(readErr, "not_found") {
						target, task.ProjectID, task.Route = domain.UnassignedProjectID, domain.UnassignedProjectID, nil
						appendTaskRuntimeDiagnostic(dashboard)
					}
				} else if !runtimeErrorIs(err, "key_unavailable") {
					appendTaskRuntimeDiagnostic(dashboard)
				}
			}
			if groups[target] == nil {
				created := &domain.ProjectDashboardItem{ID: target, Kind: "unassigned", Tasks: []domain.ProjectTask{}}
				for _, project := range dashboard.Catalog {
					if project.ID == target {
						copy := project
						created.Project = &copy
						created.Kind = "project"
						break
					}
				}
				groups[target] = created
				if target == domain.UnassignedProjectID {
					order = append([]string{target}, order...)
				} else {
					order = append(order, target)
				}
			}
			groups[target].Tasks = append(groups[target].Tasks, task)
			if target != group.ID {
				groups[target].PreferredEngineeringID = nil
				if latest := group.LatestActivity; latest != nil && (groups[target].LatestActivity == nil || latest.After(*groups[target].LatestActivity)) {
					groups[target].LatestActivity = latest
				}
			}
		}
	}
	dashboard.Projects = []domain.ProjectDashboardItem{}
	for _, identity := range order {
		group := groups[identity]
		if len(group.Tasks) != 0 || group.IsPinned {
			dashboard.Projects = append(dashboard.Projects, *group)
		}
	}
}

func runtimeErrorIs(err error, code string) bool {
	var failure *taskruntime.Error
	return errors.As(err, &failure) && failure.Code == code
}

func appendTaskRuntimeDiagnostic(dashboard *domain.ProjectDashboardSnapshot) {
	const message = "独立任务运行态读取失败；未使用不可信的任务绑定。"
	if !strings.Contains(dashboard.Message, message) {
		dashboard.Message = strings.TrimSpace(dashboard.Message + " " + message)
	}
}

func runtimeProjectID(root string, catalog []domain.Project, view taskruntime.View) string {
	if view.State.Scope != "project" || view.RouteFreshness != "current" || view.ProjectCard == "" {
		return domain.UnassignedProjectID
	}
	matched := ""
	for _, project := range catalog {
		card := filepath.ToSlash(project.CardPath)
		if filepath.IsAbs(project.CardPath) {
			relative, err := filepath.Rel(root, project.CardPath)
			if err == nil {
				card = filepath.ToSlash(relative)
			}
		}
		if project.ID == view.ProjectCard || card == view.ProjectCard {
			if matched != "" && matched != project.ID {
				return domain.UnassignedProjectID
			}
			matched = project.ID
		}
	}
	if matched == "" {
		return domain.UnassignedProjectID
	}
	return matched
}

func runtimeState(view taskruntime.View, observation domain.TaskObservation, observedAt time.Time) *domain.TaskRuntimeState {
	state := &domain.TaskRuntimeState{TaskID: view.TaskID, Revision: view.Revision, Scope: view.State.Scope, ReportedStatus: view.State.ReportedStatus, ReportedAt: view.ReportedAt, ReportFreshness: view.ReportFreshness, RouteFreshness: view.RouteFreshness, ObservedStatus: observation.RuntimeStatus, ObservedAt: observedAt}
	if view.State.Progress != nil {
		state.Progress = &domain.TaskRuntimeProgress{Summary: view.State.Progress.Summary, Percent: view.State.Progress.Percent}
	}
	return state
}

func runtimeRoute(receipt taskruntime.Receipt) *domain.RouteSummary {
	category := receipt.Projection.Category
	route := &domain.RouteSummary{Category: &domain.RouteCategory{CategoryID: &category.CategoryID, Name: &category.Name, ValidationStatus: &category.ValidationStatus, ContextPolicy: &category.ContextPolicy}, Jobs: []domain.RouteJob{}, Abilities: []domain.RouteAbility{}, DispatchableSkills: []domain.DispatchableSkill{}, ReceiptSHA256: &receipt.ReceiptSHA256}
	for _, job := range receipt.Projection.Jobs {
		route.Jobs = append(route.Jobs, domain.RouteJob{JobID: &job.JobID, Name: &job.Name, Role: &job.Role, ValidationStatus: &job.ValidationStatus, ContextPolicy: &job.ContextPolicy})
	}
	for _, ability := range receipt.Projection.Abilities {
		route.Abilities = append(route.Abilities, domain.RouteAbility{AbilityID: &ability.AbilityID, Name: &ability.Name, JobID: &ability.JobID, ValidationStatus: &ability.ValidationStatus, ContextPolicy: &ability.ContextPolicy, ResponsibilityID: &ability.ResponsibilityID})
	}
	for _, skill := range receipt.DispatchableSkills {
		route.DispatchableSkills = append(route.DispatchableSkills, domain.DispatchableSkill{SkillID: &skill.SkillID, SkillStage: &skill.SkillStage, AbilityID: &skill.AbilityID})
	}
	return route
}
