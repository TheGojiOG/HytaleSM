#!/usr/bin/env python3
"""
Hytale Server Manager - Backend Control Script

Simple script to start and stop the backend server.
"""

import os
import sys
import subprocess
import signal
import time
from pathlib import Path

# Configuration
SERVER_DIR = Path(__file__).parent
SERVER_EXECUTABLE = SERVER_DIR / "bin" / "server.exe"
SERVER_SOURCE_CMD = ["go", "run", "./cmd/server"]
PID_FILE = SERVER_DIR / "data" / "server.pid"


def get_server_pid():
    """Read the server PID from the PID file."""
    if not PID_FILE.exists():
        return None
    try:
        with open(PID_FILE, 'r') as f:
            pid = int(f.read().strip())
        # Verify the process exists
        try:
            os.kill(pid, 0)  # Signal 0 doesn't kill, just checks if process exists
            return pid
        except OSError:
            # Process doesn't exist, clean up stale PID file
            PID_FILE.unlink()
            return None
    except (ValueError, IOError):
        return None


def is_server_running():
    """Check if the server is currently running."""
    return get_server_pid() is not None


def start_server(foreground=False, use_go=False):
    """Start the backend server."""
    if is_server_running():
        print("ERROR: Server is already running (PID: {})".format(get_server_pid()))
        return False

    if not use_go and not SERVER_EXECUTABLE.exists():
        print(f"ERROR: Server executable not found: {SERVER_EXECUTABLE}")
        print("   Please build the server first with: make build")
        print("   Or start from source with: python server_control.py start --go")
        return False
    
    print("Starting Hytale Server Manager backend...")
    
    # Ensure data directory exists
    PID_FILE.parent.mkdir(parents=True, exist_ok=True)
    
    # Start the server process
    try:
        if foreground:
            # Run in foreground with visible output
            print(f"   Running in foreground mode (press Ctrl+C to stop)...")
            print("-" * 60)
            command = SERVER_SOURCE_CMD if use_go else [str(SERVER_EXECUTABLE)]
            process = subprocess.Popen(
                command,
                cwd=SERVER_DIR,
                creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if sys.platform == 'win32' else 0
            )
            
            # Save the PID
            with open(PID_FILE, 'w') as f:
                f.write(str(process.pid))
            
            try:
                # Wait for process to complete
                process.wait()
            except KeyboardInterrupt:
                print("\n\nStopping server...")
                stop_server()
            finally:
                PID_FILE.unlink(missing_ok=True)
            return True
        else:
            # Start in background (detached)
            command = SERVER_SOURCE_CMD if use_go else [str(SERVER_EXECUTABLE)]
            process = subprocess.Popen(
                command,
                cwd=SERVER_DIR,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if sys.platform == 'win32' else 0
            )
            
            # Save the PID
            with open(PID_FILE, 'w') as f:
                f.write(str(process.pid))
            
            # Wait a moment to see if it starts successfully
            time.sleep(2)
            
            if process.poll() is None:
                mode_label = "source" if use_go else "executable"
                print(f"Server started successfully ({mode_label}, PID: {process.pid})")
                print(f"   Backend running at: http://localhost:8080")
                print(f"   View logs with: python server_control.py logs")
                return True
            else:
                stdout, stderr = process.communicate()
                print("ERROR: Server failed to start")
                if stderr:
                    print(f"   Error: {stderr.decode()}")
                PID_FILE.unlink(missing_ok=True)
                return False
            
    except Exception as e:
        print(f"ERROR: Failed to start server: {e}")
        PID_FILE.unlink(missing_ok=True)
        return False


def stop_server():
    """Stop the backend server."""
    pid = get_server_pid()
    
    if pid is None:
        print("WARNING: Server is not running")
        return False
    
    print(f"Stopping server (PID: {pid})...")
    
    try:
        if sys.platform == 'win32':
            # On Windows, use taskkill
            subprocess.run(['taskkill', '/F', '/PID', str(pid)], 
                         capture_output=True, check=False)
        else:
            # On Unix-like systems, use kill
            os.kill(pid, signal.SIGTERM)
            
        # Wait for process to stop
        for _ in range(10):
            time.sleep(0.5)
            try:
                os.kill(pid, 0)
            except OSError:
                break
        else:
            # Force kill if still running
            print("   Server didn't stop gracefully, forcing...")
            if sys.platform == 'win32':
                subprocess.run(['taskkill', '/F', '/PID', str(pid)], 
                             capture_output=True, check=False)
            else:
                os.kill(pid, signal.SIGKILL)
        
        # Clean up PID file
        PID_FILE.unlink(missing_ok=True)
        print("Server stopped successfully")
        return True
        
    except Exception as e:
        print(f"ERROR: Failed to stop server: {e}")
        return False


def status_server():
    """Check server status."""
    pid = get_server_pid()
    
    if pid is None:
        print("Server Status: STOPPED")
        print(f"   Backend: http://localhost:8080 (not running)")
    else:
        print("Server Status: RUNNING")
        print(f"   PID: {pid}")
        print(f"   Backend: http://localhost:8080")
        print(f"   Logs: {SERVER_DIR / 'logs' / 'activity'}")


def restart_server():
    """Restart the backend server."""
    print("Restarting server...")
    stop_server()
    time.sleep(1)
    start_server()


def view_logs():
    """View server console output in real-time."""
    if not is_server_running():
        print("WARNING: Server is not running")
        print("   Start it with: python server_control.py start")
        return False
    
    pid = get_server_pid()
    print(f"Viewing server console output (PID: {pid})")
    print("   Press Ctrl+C to stop viewing")
    print("-" * 60)
    
    try:
        # On Windows, we can't easily tail stdout of a running process
        # So we'll use PowerShell to get process output or check logs
        if sys.platform == 'win32':
            # Try to find log files or just show a message
            logs_dir = SERVER_DIR / "logs" / "activity"
            log_files = list(logs_dir.glob("*.log")) if logs_dir.exists() else []
            
            if log_files:
                # Tail the most recent log file
                latest_log = max(log_files, key=lambda p: p.stat().st_mtime)
                print(f"Tailing: {latest_log.name}\n")
                
                # Use PowerShell Get-Content with -Wait flag
                subprocess.run([
                    'powershell', '-Command',
                    f'Get-Content "{latest_log}" -Tail 50 -Wait'
                ])
            else:
                print("WARNING: No log files found. Server is logging to stdout.")
                print(f"   Server running at: http://localhost:8080")
                print(f"   PID: {pid}")
                print("\nTip: Restart server in foreground mode to see console:")
                print("   python server_control.py stop")
                print("   python server_control.py start --foreground")
        else:
            # On Unix-like systems, try to tail stdout/stderr
            print("WARNING: Background process output not available")
            print(f"   Server running at: http://localhost:8080")
            print("\nTip: Restart server in foreground mode to see console:")
            print("   python server_control.py stop")
            print("   python server_control.py start --foreground")
            
    except KeyboardInterrupt:
        print("\n\nStopped viewing logs")
    except Exception as e:
        print(f"ERROR: Error viewing logs: {e}")
    
    return True


def show_help():
    """Show usage information."""
    print("""
Hytale Server Manager - Backend Control Script

Usage:
    python server_control.py [command] [options]

Commands:
    start              - Start the backend server in background
    start --foreground - Start the server with visible console output
    start -f           - Same as --foreground
    start --go          - Start the server from source (go run)
    start -g            - Same as --go
    start --go -f       - Start from source with visible console output
    stop               - Stop the backend server
    restart            - Restart the backend server
    status             - Check server status
    logs               - View server console output (if available)
    console            - Same as logs
    help               - Show this help message

Default Admin Credentials:
    Username: admin
    Password: (set when creating admin user)

Examples:
    python server_control.py start
    python server_control.py start --foreground
    python server_control.py start --go --foreground
    python server_control.py logs
    python server_control.py stop
    python server_control.py status
""")


def main():
    """Main entry point."""
    if len(sys.argv) < 2:
        command = "status"
    else:
        command = sys.argv[1].lower()
    
    # Check for foreground flag
    foreground = False
    use_go = False
    if command == 'start' and len(sys.argv) > 2:
        for flag in sys.argv[2:]:
            flag = flag.lower()
            if flag in ['--foreground', '-f', 'foreground']:
                foreground = True
            if flag in ['--go', '-g', 'go', '--source']:
                use_go = True
    
    commands = {
        'start': lambda: start_server(foreground, use_go),
        'stop': stop_server,
        'restart': restart_server,
        'status': status_server,
        'logs': view_logs,
        'console': view_logs,
        'help': show_help,
        '--help': show_help,
        '-h': show_help,
    }
    
    if command in commands:
        commands[command]()
    else:
        print(f"ERROR: Unknown command: {command}")
        print("   Use 'python server_control.py help' for usage information")
        sys.exit(1)


if __name__ == '__main__':
    main()
