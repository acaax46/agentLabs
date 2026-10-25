FROM opensandbox/server@sha256:bcd5f264101262cebfa7e1ff9cd0424403e42f31a6596e4364cf90d8cb5c936b AS python
FROM sandbox-registry.cn-zhangjiakou.cr.aliyuncs.com/opensandbox/execd@sha256:cda2bc35409bbf57561599b316cafb899598f7dce671332e7ceda14eb1cfeded AS execd
FROM agentlab/vm-smoke:alpine-3.22 AS pack
USER root
COPY --from=python / /guestroot/
COPY --from=execd /execd /guestroot/execd
COPY --from=execd /opt/opensandbox /guestroot/opt/opensandbox
COPY vendor/agentlab-guest /guestroot/agentlab-guest
COPY init /guestroot/init
RUN cp /bin/busybox /guestroot/bin/busybox && chmod 755 /guestroot/init /guestroot/agentlab-guest \
 && mkdir -p /guestroot/run/control /guestroot/workspace \
 && chmod 700 /guestroot/run/control \
 && cd /guestroot && find . -print0 | cpio --null -o --format=newc 2>/dev/null | gzip -1 > /opt/agentlab-vm/guest-initramfs.gz \
 && rm -rf /guestroot
LABEL org.agentlab.guest-protocol="1" org.agentlab.guest-runtime="opensandbox-execd-1.1.1"
USER node
ENTRYPOINT ["timeout","--signal=KILL","660","qemu-system-x86_64","-machine","q35,accel=tcg","-cpu","max","-m","1024","-smp","1","-kernel","/opt/agentlab-vm/vmlinuz-virt","-initrd","/opt/agentlab-vm/guest-initramfs.gz","-append","console=ttyS0 quiet loglevel=0 panic=-1","-display","none","-monitor","none","-serial","stdio","-no-reboot","-nic","none"]
