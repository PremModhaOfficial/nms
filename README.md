[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/PremModhaOfficial/nms)
# Network Management System (NMS) - Lite

A lightweight, extensible network management system for discovery, monitoring, and metric collection.

## Getting Started

Follow these steps to set up and run the NMS on a new machine.

### Prerequisites

- **Go:** 1.21 or higher
- **PostgreSQL:** Ensure you have a running instance.
- **Python 3:** For database seeding (optional).
- **Make:** For using the automation commands.

### 1. Database Setup

1.  Create a PostgreSQL database named `nms` on your local or remote server.
2.  Initialize the database schema manually by running the `schema.sql` script:
    ```bash
    psql -h localhost -U your_user -d nms -f schema.sql
    ```

*Note: Ensure your database connection settings in `.env` match your local PostgreSQL credentials.*

### 2. Configuration

Create or update your `.env` file with the necessary environment variables:
- `DB_URL`: PostgreSQL connection string.
- `JWT_SECRET`: Secret key for token generation.
- `ENCRYPTION_KEY`: 32-character key for data encryption.

### 3. Build and Run

The project uses a `Makefile` to simplify common tasks. For a completely new environment, use the `first-run` command:

- **Full Setup and Run (New Environment):**
  ```bash
  make first-run
  ```
  *This command stops any existing instance, initializes the database schema from `schema.sql`, builds the server and plugins, and starts the application.*

- **Build the application and plugins:**
  ```bash
  make build
  ```
- **Run in development mode (rebuilds and starts):**
  ```bash
  make dev
  ```
- **Stop the application:**
  ```bash
  make stop
  ```

### 4. Available Makefile Commands

| Command | Description |
| :--- | :--- |
| `make first-run` | Full setup: Stop -> DB Setup -> Build -> Run. |
| `make db-setup` | Initializes the database schema from `schema.sql`. |
| `make build` | Compiles the main server and all plugins. |
| `make dev` | Stops any running instance, rebuilds, and starts the server. |
| `make run` | Starts the server using the `start.sh` script. |
| `make stop` | Terminate the server process and frees up the port (default 8080). |
| `make seed` | Populates the database with initial sample data. |
| `make clean` | Removes compiled binaries. |

## Project Structure

- `cmd/app/`: Application entry point.
- `pkg/api/`: REST API implementation and middleware.
- `pkg/polling/`: Core metric collection logic.
- `pkg/scheduling/`: Task scheduling system.
- `plugin-code/`: Source code for polling plugins.
- `plugins/`: Compiled plugin binaries.

## Security

The API is protected using JWT authentication. Ensure you have a valid token (generated via the admin login) to interact with the management endpoints.
