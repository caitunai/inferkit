#!/bin/sh

# Install this file with mode 0600 in a user-level configuration directory and
# source it from an interactive shell startup file. It defines an `atlas`
# function that loads the .env next to the nearest atlas.hcl before invoking the
# real Atlas executable.
atlas() {
	(
		_atlas_project_dir=$PWD
		while [ "$_atlas_project_dir" != "/" ] && [ ! -f "$_atlas_project_dir/atlas.hcl" ]; do
			_atlas_parent_dir=$(dirname "$_atlas_project_dir")
			if [ "$_atlas_parent_dir" = "$_atlas_project_dir" ]; then
				break
			fi
			_atlas_project_dir=$_atlas_parent_dir
		done

		if [ -f "$_atlas_project_dir/atlas.hcl" ] && [ -f "$_atlas_project_dir/.env" ]; then
			set -a
			# shellcheck disable=SC1090 # The project-local path is resolved at runtime.
			if ! . "$_atlas_project_dir/.env"; then
				printf '%s\n' "atlas: failed to load $_atlas_project_dir/.env" >&2
				exit 1
			fi
			set +a
		fi

		command atlas "$@"
	)
}
