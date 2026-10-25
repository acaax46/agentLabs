ARG AGENT_IMAGE=agentlab/agents:validated-20261023
FROM ${AGENT_IMAGE}
USER root
COPY vendor/cc-switch.deb /tmp/cc-switch.deb
COPY vendor/cc-switch.deb.sha256 /tmp/cc-switch.deb.sha256
RUN cd /tmp && sha256sum -c cc-switch.deb.sha256 \
    && apt-get update && apt-get install -y --no-install-recommends \
      /tmp/cc-switch.deb xvfb x11vnc novnc websockify openbox dbus-x11 x11-utils fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/* /tmp/cc-switch.deb /tmp/cc-switch.deb.sha256
COPY --chmod=755 start.sh /usr/local/bin/agentlab-cc-switch
COPY profile_tool.py /opt/agentlab/profile_tool.py
USER node
EXPOSE 6080
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/agentlab-cc-switch"]
