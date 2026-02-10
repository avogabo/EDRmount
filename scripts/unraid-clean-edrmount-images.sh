#!/usr/bin/env bash
set -euo pipefail

# Clean up old/dangling images ONLY for EDRmount.
# Safe: does not touch volumes; only removes images not used by any container.
# Usage (on Unraid): bash unraid-clean-edrmount-images.sh

REPO_REGEX='^ghcr\.io/avogabo/edrmount$'

# Image IDs used by any container (running or stopped)
USED_IDS=$(docker ps -a --format '{{.Image}}' | sed 's/@sha256:.*$//' | sort -u)

# Candidate image IDs for this repo (all tags)
mapfile -t CAND < <(docker images --format '{{.Repository}} {{.ID}} {{.Tag}}' \
  | awk -v re="$REPO_REGEX" '$1 ~ re {print $2" "$3}' \
  | sort -u)

if [[ ${#CAND[@]} -eq 0 ]]; then
  echo "No EDRmount images found."
  exit 0
fi

# Keep the current latest if present
LATEST_ID=$(docker images --format '{{.Repository}} {{.Tag}} {{.ID}}' | awk '$1=="ghcr.io/avogabo/edrmount" && $2=="latest" {print $3; exit}')

TO_REMOVE=()
for line in "${CAND[@]}"; do
  id=$(awk '{print $1}' <<<"$line")
  tag=$(awk '{print $2}' <<<"$line")

  # keep latest image id
  if [[ -n "${LATEST_ID:-}" && "$id" == "$LATEST_ID" ]]; then
    continue
  fi

  # skip images in use by any container
  if grep -qx "$id" <<<"$USED_IDS"; then
    continue
  fi

  TO_REMOVE+=("$id")
done

if [[ ${#TO_REMOVE[@]} -eq 0 ]]; then
  echo "No unused EDRmount images to remove (kept latest + in-use images)."
  exit 0
fi

echo "Removing unused EDRmount image IDs:" >&2
printf ' - %s\n' "${TO_REMOVE[@]}" >&2

# Remove unique IDs
printf '%s\n' "${TO_REMOVE[@]}" | sort -u | xargs -r docker rmi

echo "Done."
