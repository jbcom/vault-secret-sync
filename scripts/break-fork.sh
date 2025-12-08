#!/bin/bash
# break-fork.sh - Script to break the fork and rename to secretsync
#
# Usage: ./scripts/break-fork.sh [new-org] [new-repo]
# Example: ./scripts/break-fork.sh jbcom secretsync

set -euo pipefail

NEW_ORG="${1:-jbcom}"
NEW_REPO="${2:-secretsync}"
OLD_MODULE="github.com/robertlestak/vault-secret-sync"
NEW_MODULE="github.com/${NEW_ORG}/${NEW_REPO}"

echo "=== Breaking Fork: vault-secret-sync → ${NEW_REPO} ==="
echo ""
echo "Old module: ${OLD_MODULE}"
echo "New module: ${NEW_MODULE}"
echo ""

# Confirm
read -p "Continue? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 1
fi

echo ""
echo "Step 1: Updating go.mod..."
sed -i "s|module ${OLD_MODULE}|module ${NEW_MODULE}|g" go.mod

echo "Step 2: Updating all Go imports..."
find . -name "*.go" -type f -exec sed -i "s|${OLD_MODULE}|${NEW_MODULE}|g" {} +

echo "Step 3: Updating documentation..."
find . -name "*.md" -type f -exec sed -i "s|${OLD_MODULE}|${NEW_MODULE}|g" {} +
find . -name "*.md" -type f -exec sed -i "s|vault-secret-sync|${NEW_REPO}|g" {} +

echo "Step 4: Updating Helm charts..."
find deploy/charts -name "*.yaml" -type f -exec sed -i "s|vault-secret-sync|${NEW_REPO}|g" {} +
find deploy/charts -name "Chart.yaml" -type f -exec sed -i "s|vault-secret-sync|${NEW_REPO}|g" {} +

echo "Step 5: Updating Dockerfile..."
sed -i "s|vault-secret-sync|${NEW_REPO}|g" Dockerfile

echo "Step 6: Updating GitHub workflows..."
find .github -name "*.yml" -type f -exec sed -i "s|vault-secret-sync|${NEW_REPO}|g" {} +

echo "Step 7: Running go mod tidy..."
go mod tidy

echo "Step 8: Verifying build..."
go build ./...

echo ""
echo "=== Fork Break Complete ==="
echo ""
echo "Next steps:"
echo "1. Create new GitHub repo: https://github.com/${NEW_ORG}/${NEW_REPO}"
echo "   - Do NOT create as a fork"
echo "   - Create empty (no README, no .gitignore)"
echo ""
echo "2. Remove old git history and push fresh:"
echo "   rm -rf .git"
echo "   git init"
echo "   git add -A"
echo "   git commit -m 'Initial commit: ${NEW_REPO} - Universal Secrets Sync'"
echo "   git remote add origin https://github.com/${NEW_ORG}/${NEW_REPO}.git"
echo "   git push -u origin main"
echo ""
echo "3. (Optional) Archive old repo with redirect notice"
echo ""
echo "4. Update Docker Hub / container registry"
echo ""
echo "5. Update Helm chart repository"
