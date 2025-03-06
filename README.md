# tf-os

tf-os is a command-line tool that connects to OpenSearch instances via SSH tunnels. It allows secure and convenient access to OpenSearch clusters, making it easier to interact with remote search services.

## Features
- Establishes secure SSH tunnels to OpenSearch instances
- Supports multiple configurations
- Easy-to-use CLI commands

## Prerequisites
Before using `tf-os`, ensure you have the following installed:
- Go (if building from source)
- OpenSSH client
- OpenSearch
- Make (for using the Makefile commands)

## Installation
You can build the project from source using `make`:

```sh
make build
```

This will compile the `tf-os` binary inside the `bin/` directory.

## Usage
To start an SSH tunnel to an OpenSearch instance:

```sh
./bin/tf-os connect --host <OPENSEARCH_HOST> --user <SSH_USER> --key <PRIVATE_KEY_PATH>
```

You can specify different options based on your OpenSearch setup.

## Makefile Commands
The provided Makefile includes several useful commands:

- **Build the binary:**
  ```sh
  make build
  ```
  This compiles the source code into an executable.

- **Run the application:**
  ```sh
  make run
  ```
  This builds and executes the application.

- **Clean up compiled files:**
  ```sh
  make clean
  ```
  Removes the `bin/` directory and compiled files.

- **Format the code:**
  ```sh
  make fmt
  ```
  Formats the Go source code according to best practices.

## Configuration
You can configure `tf-os` using a YAML configuration file stored at `~/.config/tf/config.yaml`. The configuration file should include details such as OpenSearch endpoints and SSH credentials.

Example configuration:
```yaml
bastion_host: ubuntu@12.34.56.78
key_path: ~/.ssh/key.pem
environments:
  production:
    opensearch_host: hostname.com
  staging:
    opensearch_host: hostname.com

```

## Contributing
Contributions are welcome! Feel free to submit pull requests or open issues.

## License
This project is licensed under the MIT License.


