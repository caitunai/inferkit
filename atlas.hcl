# Variables for migration environment
variable "dev_username" {
  type    = string
  default = getenv("DB_DEV_USERNAME")
}

variable "dev_password" {
  type    = string
  default = getenv("DB_DEV_PASSWORD")
}

variable "dev_host" {
  type    = string
  default = getenv("DB_DEV_HOST")
}

variable "dev_port" {
  type    = string
  default = getenv("DB_DEV_PORT")
}

variable "dev_db" {
  type    = string
  default = getenv("DB_DEV_NAME")
}

# Local database from environment variables
variable "local_username" {
  type    = string
  default = getenv("DB_LOCAL_USERNAME")
}

variable "local_password" {
  type    = string
  default = getenv("DB_LOCAL_PASSWORD")
}

variable "local_host" {
  type    = string
  default = getenv("DB_LOCAL_HOST")
}

variable "local_port" {
  type    = string
  default = getenv("DB_LOCAL_PORT")
}

variable "local_db" {
  type    = string
  default = getenv("DB_LOCAL_NAME")
}

# Production from environment variables
variable "prod_username" {
  type    = string
  default = getenv("DB_PROD_USERNAME")
}

variable "prod_password" {
  type    = string
  default = getenv("DB_PROD_PASSWORD")
}

variable "prod_host" {
  type    = string
  default = getenv("DB_PROD_HOST")
}

variable "prod_port" {
  type    = string
  default = getenv("DB_PROD_PORT")
}

variable "prod_db" {
  type    = string
  default = getenv("DB_PROD_NAME")
}

# Development environment to generate migrations
env "dev" {
  dev = "mysql://${var.dev_username}:${var.dev_password}@${var.dev_host}:${var.dev_port}/${var.dev_db}"

  migration {
    dir = "file://atlas/migrations"
  }

  # The schema directory includes all .hcl files
  schema {
    src = "file://atlas/schema"
  }
}

# Local environment to migrate
env "local" {
  dev = "mysql://${var.dev_username}:${var.dev_password}@${var.dev_host}:${var.dev_port}/${var.dev_db}"
  url = "mysql://${var.local_username}:${var.local_password}@${var.local_host}:${var.local_port}/${var.local_db}"

  migration {
    dir = "file://atlas/migrations"
  }

  # The schema directory includes all .hcl files
  schema {
    src = "file://atlas/schema"
  }
}

# Product environment to migrate
env "prod" {
  dev = "mysql://${var.dev_username}:${var.dev_password}@${var.dev_host}:${var.dev_port}/${var.dev_db}"
  url = "mysql://${var.prod_username}:${var.prod_password}@${var.prod_host}:${var.prod_port}/${var.prod_db}"

  migration {
    dir = "file://atlas/migrations"
  }

  # The schema directory includes all .hcl files
  schema {
    src = "file://atlas/schema"
  }
}
