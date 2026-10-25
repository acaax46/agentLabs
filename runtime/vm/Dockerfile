ARG NODE_IMAGE=node:22.20.0-bookworm-slim@sha256:b21fe589dfbe5cc39365d0544b9be3f1f33f55f3c86c87a76ff65a02f8f5848e
FROM ${NODE_IMAGE}
USER root
RUN apt-get update && apt-get install -y --no-install-recommends qemu-system-x86 busybox-static cpio \
    && rm -rf /var/lib/apt/lists/*
COPY vendor/vmlinuz-virt /opt/agentlab-vm/vmlinuz-virt
COPY vendor/vmlinuz-virt.sha256 /opt/agentlab-vm/vmlinuz-virt.sha256
COPY init /opt/agentlab-init
RUN cd /opt/agentlab-vm && sha256sum -c vmlinuz-virt.sha256 \
    && mkdir -p /tmp/initroot/bin /tmp/initroot/sbin /tmp/initroot/proc /tmp/initroot/sys /tmp/initroot/dev \
    && cp /bin/busybox /tmp/initroot/bin/busybox \
    && ln -s busybox /tmp/initroot/bin/sh \
    && cp /opt/agentlab-init /tmp/initroot/init && chmod 755 /tmp/initroot/init \
    && cd /tmp/initroot && find . -print0 | cpio --null -o --format=newc | gzip -9 > /opt/agentlab-vm/initramfs.gz \
    && rm -rf /tmp/initroot
USER node
ENTRYPOINT ["qemu-system-x86_64", "-machine", "q35,accel=tcg", "-cpu", "max", "-m", "256", "-smp", "1", "-kernel", "/opt/agentlab-vm/vmlinuz-virt", "-initrd", "/opt/agentlab-vm/initramfs.gz", "-append", "console=ttyS0 quiet panic=1", "-nographic", "-no-reboot", "-nic", "none"]
