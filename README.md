# Quillbridge

Quillbridge is a Go-based backend designed to emulate the NextCloud Notes API, enabling seamless integration with Quillpad and similar note-taking applications. It provides a lightweight, self-hosted solution for managing notes with a focus on simplicity and compatibility.

## Features

- **NextCloud Notes API Emulation**: Fully compatible with Quillpad and other clients supporting the NextCloud Notes API.
- **SQLite Database**: Lightweight and efficient storage for your notes.
- **Admin User Management**: Automatically seeds an admin user for initial setup.
- **Middleware Support**: Includes logging, path scrubbing, and other middleware for enhanced functionality.
- **Extensible Handlers**: Modular design for adding custom endpoints.

## Getting Started

### Prerequisites

- Go 1.26.1 or higher
- Docker (optional, for containerized deployment)

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/dermetti/quillbridge.git
   cd quillbridge
   ```

2. Build the project:
   ```bash
   go build -o quillbridge ./cmd/quillbridge
   ```

3. Run the application:
   ```bash
   ./quillbridge
   ```

   By default, the application creates a `./data` directory and initializes an SQLite database.

### Default Admin Credentials

- **Username**: `quillbridgeadmin`
- **Password**: `quillbridgepass`

> **Note**: Change these credentials immediately after the first login.

## Configuration

- **Data Directory**: The application uses `./data` by default. You can modify this in the `main.go` file or through environment variables.
- **Database**: SQLite is used for storage. The database file is located at `./data/quillbridge.db`.

## API Endpoints

- **Capabilities**: `/ocs/v1.php/cloud/capabilities`  
  Required by Quillpad during the initial connection.

- **Notes Management**: `/ocs/v1.php/apps/notes/api/v1/notes`  
  Handles CRUD operations for notes.

## Development

### Dependencies

The project uses the following Go modules:

- [go-chi/chi](https://github.com/go-chi/chi): HTTP router for building Go services.
- [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto): Cryptographic utilities.
- [modernc.org/sqlite](https://modernc.org/sqlite): SQLite driver for Go.

Install dependencies with:
```bash
go mod tidy
```

### Running Tests

Run the test suite with:
```bash
go test ./...
```

## Docker Deployment

1. Build the Docker image:
   ```bash
   docker build -t quillbridge .
   ```

2. Run the container:
   ```bash
   docker-compose up
   ```

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please open an issue or submit a pull request for any changes.
