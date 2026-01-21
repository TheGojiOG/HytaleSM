package systemd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

type Event struct {
	Service   string
	OldState  string
	NewState  string
	Timestamp int64
}

func Watch(ctx context.Context, services []string, onEvent func(Event)) error {
	if len(services) == 0 {
		return nil
	}
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("system bus: %w", err)
	}

	tracked := make(map[dbus.ObjectPath]string, len(services))
	stateCache := make(map[string]string, len(services))

	for _, svc := range services {
		path, err := getUnitPath(conn, svc)
		if err != nil {
			continue
		}
		tracked[path] = svc
		if st, err := getActiveState(conn, path); err == nil {
			stateCache[svc] = st
		}
	}

	matchProps := "type='signal',sender='org.freedesktop.systemd1',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged'"
	matchJobs := "type='signal',sender='org.freedesktop.systemd1',interface='org.freedesktop.systemd1.Manager',member='JobRemoved'"
	_ = conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchProps)
	_ = conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchJobs)

	sigCh := make(chan *dbus.Signal, 64)
	conn.Signal(sigCh)

	for {
		select {
		case <-ctx.Done():
			return nil
		case sig := <-sigCh:
			if sig == nil {
				continue
			}
			switch sig.Name {
			case "org.freedesktop.DBus.Properties.PropertiesChanged":
				svc, ok := tracked[sig.Path]
				if !ok {
					continue
				}
				if len(sig.Body) < 2 {
					continue
				}
				changed, ok := sig.Body[1].(map[string]dbus.Variant)
				if !ok {
					continue
				}
				variant, ok := changed["ActiveState"]
				if !ok {
					continue
				}
				newState, _ := variant.Value().(string)
				oldState := stateCache[svc]
				if newState != "" && newState != oldState {
					stateCache[svc] = newState
					onEvent(Event{Service: svc, OldState: oldState, NewState: newState, Timestamp: time.Now().Unix()})
				}
			case "org.freedesktop.systemd1.Manager.JobRemoved":
				if len(sig.Body) < 4 {
					continue
				}
				unit, _ := sig.Body[2].(string)
				if unit == "" {
					continue
				}
				unit = strings.TrimSpace(unit)
				for _, svc := range services {
					if svc != unit {
						continue
					}
					path, err := getUnitPath(conn, svc)
					if err != nil {
						continue
					}
					newState, err := getActiveState(conn, path)
					if err != nil {
						continue
					}
					oldState := stateCache[svc]
					if newState != oldState {
						stateCache[svc] = newState
						onEvent(Event{Service: svc, OldState: oldState, NewState: newState, Timestamp: time.Now().Unix()})
					}
				}
			}
		}
	}
}

func Snapshot(services []string) (map[string]string, error) {
	result := make(map[string]string)
	if len(services) == 0 {
		return result, nil
	}
	conn, err := dbus.SystemBus()
	if err != nil {
		return result, err
	}
	for _, svc := range services {
		path, err := getUnitPath(conn, svc)
		if err != nil {
			continue
		}
		state, err := getActiveState(conn, path)
		if err != nil {
			continue
		}
		result[svc] = state
	}
	return result, nil
}

func getUnitPath(conn *dbus.Conn, service string) (dbus.ObjectPath, error) {
	obj := conn.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1")
	call := obj.Call("org.freedesktop.systemd1.Manager.GetUnit", 0, service)
	if call.Err != nil {
		return "", call.Err
	}
	path, ok := call.Body[0].(dbus.ObjectPath)
	if !ok {
		return "", fmt.Errorf("unexpected unit path type")
	}
	return path, nil
}

func getActiveState(conn *dbus.Conn, path dbus.ObjectPath) (string, error) {
	obj := conn.Object("org.freedesktop.systemd1", path)
	variant, err := obj.GetProperty("org.freedesktop.systemd1.Unit.ActiveState")
	if err != nil {
		return "", err
	}
	state, _ := variant.Value().(string)
	return state, nil
}
