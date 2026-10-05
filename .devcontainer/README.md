# Dashboard development container

Open this repository in a Dev Containers-compatible editor and choose Reopen in
Container. The definition pins Go 1.26.6, Node 26.5.1 and npm 11.17.0, with locked
Docker-outside-of-Docker and GitHub CLI features. `make setup` installs repository
quality tools and locked dependencies. Python 3, ShellCheck, PostgreSQL client,
Git and editor integrations are included.

The container supports Go services, React and contract generation. Run
`make web dev`, then use the editor's **private** port forwarding for 8088.
Vite uses port 5173. Never make either forwarded fixture port public. The
Docker socket feature grants access to the host Docker daemon; use only trusted
repository code and do not mount production credentials.

The root Compose file starts a separate synthetic PostgreSQL service; from a
container, use its network hostname or a deliberately configured host gateway,
not the container's own 127.0.0.1. The default host-side test command is in
[testing](../docs/TESTING.md). No production service is launched automatically.

Native SwiftUI/iPhone builds require macOS and Xcode outside this Linux container.
Keep Apple signing material on the authorized Mac. See [iOS development](../ios/README.md).
