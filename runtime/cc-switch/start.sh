#!/bin/sh
set -eu
export DISPLAY=:99
export XDG_RUNTIME_DIR=/tmp/agentlab-xdg
mkdir -p "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
python3 /opt/agentlab/profile_tool.py seed
Xvfb :99 -screen 0 1280x900x24 -nolisten tcp &
xvfb_pid=$!
for attempt in $(seq 1 40); do
  if xdpyinfo -display :99 >/dev/null 2>&1; then break; fi
  sleep 0.25
done
xdpyinfo -display :99 >/dev/null
openbox >/tmp/openbox.log 2>&1 &
openbox_pid=$!
x11vnc -display :99 -localhost -forever -shared -nopw -rfbport 5900 >/tmp/x11vnc.log 2>&1 &
vnc_pid=$!
websockify --web=/usr/share/novnc 6080 localhost:5900 >/tmp/novnc.log 2>&1 &
web_pid=$!
trap 'kill "$web_pid" "$vnc_pid" "$openbox_pid" "$xvfb_pid" 2>/dev/null || true' EXIT INT TERM
dbus-run-session -- cc-switch
