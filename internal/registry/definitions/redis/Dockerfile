ARG IMAGE=redis:8.8.0
FROM ${IMAGE}

# the export sequence is vendored rather than run from the host, so that the
# dump is streamed out of the container instead of buffered through the tool
COPY rootfs/ /
