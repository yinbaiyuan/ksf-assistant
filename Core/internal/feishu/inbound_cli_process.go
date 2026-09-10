package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var CLIManagedEventKeys = []string{"im.message.receive_v1", "card.action.trigger"}

type cliBusStatus struct {
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	Active    int    `json:"active_consumers"`
	Consumers []struct {
		PID      int    `json:"pid"`
		EventKey string `json:"event_key"`
		Dropped  int    `json:"dropped"`
	} `json:"consumers"`
}

func (inbound *OfficialInbound) busStatus(ctx context.Context) (cliBusStatus, error) {
	value, err := inbound.runner.runCLIJSON(ctx, []string{"event", "status", "--current", "--json", "--fail-on-orphan"}, nil, inbound.runner.WorkingDirectory, 10*time.Second)
	if err != nil {
		return cliBusStatus{}, err
	}
	data, _ := json.Marshal(value)
	var status struct {
		Apps []cliBusStatus `json:"apps"`
	}
	if json.Unmarshal(data, &status) != nil || len(status.Apps) != 1 {
		return cliBusStatus{}, errors.New("cli_event_status_invalid")
	}
	return status.Apps[0], nil
}

func (inbound *OfficialInbound) runConsumers(ctx context.Context) error {
	initial, err := inbound.busStatus(ctx)
	if err != nil {
		return err
	}
	if initial.Status != "not_running" {
		return errors.New("cli_event_existing_bus_conflict")
	}
	runCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	pids := map[int]bool{}
	ownedBus := 0
	defer func() {
		if ownedBus == 0 {
			inspectCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			current, inspectErr := inbound.busStatus(inspectCtx)
			stop()
			if inspectErr == nil && current.Status == "running" && len(current.Consumers) > 0 {
				ours := true
				for _, consumer := range current.Consumers {
					ours = ours && pids[consumer.PID]
				}
				if ours {
					ownedBus = current.PID
				}
			}
		}
		cancel()
		workers.Wait()
		if ownedBus == 0 {
			return
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 12*time.Second)
		defer stop()
		current, err := inbound.busStatus(cleanupCtx)
		if err != nil || current.PID != ownedBus || current.Active != 0 {
			return
		}
		_, _ = inbound.runner.runCLIJSON(cleanupCtx, []string{"event", "stop", "--json"}, nil, inbound.runner.WorkingDirectory, 8*time.Second)
	}()
	ready := make(chan string, len(CLIManagedEventKeys))
	failures := make(chan error, len(CLIManagedEventKeys)*2)
	for _, key := range CLIManagedEventKeys {
		args := []string{}
		if inbound.runner.Profile != "" {
			args = append(args, "--profile", inbound.runner.Profile)
		}
		args = append(args, "event", "consume", key, "--as", "bot")
		command := exec.CommandContext(runCtx, inbound.runner.Binary, args...)
		command.Env = authEnvironment(inbound.runner.DataRoot)
		command.Dir = inbound.runner.WorkingDirectory
		stdin, err := command.StdinPipe()
		if err != nil {
			return err
		}
		stdout, stdoutWriter := io.Pipe()
		stderr, stderrWriter := io.Pipe()
		command.Stdout, command.Stderr = stdoutWriter, stderrWriter
		command.Cancel = func() error { return stdin.Close() }
		command.WaitDelay = 5 * time.Second
		if err := command.Start(); err != nil {
			_ = stdout.Close()
			_ = stdoutWriter.Close()
			_ = stderr.Close()
			_ = stderrWriter.Close()
			return err
		}
		waited := make(chan error, 1)
		go func() {
			waited <- command.Wait()
			_ = stdoutWriter.Close()
			_ = stderrWriter.Close()
		}()
		pids[command.Process.Pid] = true
		workers.Add(1)
		go func(key string, command *exec.Cmd, stdout, stderr io.ReadCloser) {
			defer workers.Done()
			defer stdout.Close()
			defer stderr.Close()
			diagnosticsDone := make(chan struct{})
			go func() {
				defer close(diagnosticsDone)
				scanner := bufio.NewScanner(stderr)
				scanner.Buffer(make([]byte, 4096), maximumCapabilityErrorBytes)
				announced := false
				for scanner.Scan() {
					line := scanner.Text()
					if line == "[event] ready event_key="+key && !announced {
						announced = true
						ready <- key
					}
					// The official bus owns network reconnection. A reconnect notice
					// does not mean events were dropped or our consumers exited.
					if strings.Contains(line, "reconnecting") {
						_ = NewDiagnosticLog(inbound.runner.DataRoot).Record(SupervisorDiagnostic{Code: "cli_event_bus_reconnecting", Component: "event-consumers", SafeSummary: "official event bus is reconnecting"})
					}
					if strings.Contains(line, "drop") {
						select {
						case failures <- errors.New("cli_event_delivery_diagnostic"):
						default:
						}
					}
				}
				if scanner.Err() != nil {
					select {
					case failures <- errors.New("cli_event_diagnostic_limit"):
					default:
					}
				}
			}()
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 4096), maximumCapabilityOutputBytes)
			for scanner.Scan() {
				if err := inbound.HandleCLIEvent(runCtx, key, append([]byte(nil), scanner.Bytes()...)); err != nil {
					select {
					case failures <- errors.New("cli_event_normalization_or_persistence_failed"):
					default:
					}
					cancel()
					break
				}
			}
			readErr := scanner.Err()
			if readErr != nil {
				cancel()
			}
			_ = stdout.Close()
			<-diagnosticsDone
			waitErr := <-waited
			if runCtx.Err() == nil {
				if readErr != nil || waitErr != nil {
					select {
					case failures <- errors.New("cli_event_consumer_failed"):
					default:
					}
				} else {
					select {
					case failures <- errors.New("cli_event_consumer_exited"):
					default:
					}
				}
			}
		}(key, command, stdout, stderr)
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for count := 0; count < len(CLIManagedEventKeys); count++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case <-timer.C:
			return errors.New("cli_event_ready_timeout")
		case <-ready:
		}
	}
	current, err := inbound.busStatus(ctx)
	if err != nil {
		return err
	}
	if err := verifyOwnedCLIConsumers(current, pids); err != nil {
		return err
	}
	ownedBus = current.PID
	inbound.observer("connected")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	statusFailures := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case <-ticker.C:
			current, err := inbound.busStatus(ctx)
			if err != nil {
				statusFailures++
				if statusFailures == 1 {
					inbound.observer("reconnecting")
				}
				if statusFailures >= 3 {
					return errors.New("cli_event_status_unavailable")
				}
				continue
			}
			if current.PID != ownedBus {
				return errors.New("cli_event_bus_replaced")
			}
			if err := verifyOwnedCLIConsumers(current, pids); err != nil {
				return err
			}
			if statusFailures > 0 {
				statusFailures = 0
				inbound.observer("connected")
			}
		}
	}
}

func verifyOwnedCLIConsumers(status cliBusStatus, pids map[int]bool) error {
	if status.Status != "running" || status.PID <= 0 || status.Active != len(pids) || len(status.Consumers) != len(pids) {
		return errors.New("cli_event_consumer_set_mismatch")
	}
	seen := map[int]bool{}
	for _, consumer := range status.Consumers {
		if !pids[consumer.PID] || seen[consumer.PID] || consumer.Dropped > 0 {
			return errors.New("cli_event_consumer_conflict_or_loss")
		}
		seen[consumer.PID] = true
	}
	return nil
}
