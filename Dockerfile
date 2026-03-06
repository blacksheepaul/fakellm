# FakeLLM - Minimal Docker image using pre-compiled binary
# This uses scratch for smallest possible image size
FROM scratch

# Set working directory
WORKDIR /

# Copy the pre-compiled Linux binary
COPY bin/fakellm-linux-amd64 /fakellm

# Expose the default port
EXPOSE 8080

# Run the server
ENTRYPOINT ["/fakellm"]
CMD ["-addr", ":8080"]

# Labels for metadata
LABEL org.opencontainers.image.title="FakeLLM"
LABEL org.opencontainers.image.description="Programmable OpenAI-Compatible Mock LLM Server"
LABEL org.opencontainers.image.source="https://github.com/blacksheepaul/fakellm"
