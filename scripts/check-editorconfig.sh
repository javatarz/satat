#!/bin/bash
# Script to check editorconfig compliance and provide feedback

echo "Checking editorconfig compliance..."

# Run editorconfig-checker on all files
RESULT=$(pre-commit run editorconfig-checker --all-files 2>&1)

if [ $? -eq 0 ]; then
    echo "✅ All files are editorconfig compliant!"
    exit 0
else
    echo "❌ Found editorconfig compliance issues:"
    echo "$RESULT"
    echo ""
    echo "To fix these issues automatically, you can:"
    echo "1. Install editorconfig tools: npm install -g editorconfig"
    echo "2. Run: editorconfig-format <file> to fix individual files"
    echo "3. Or manually fix the issues shown above"
    exit 1
fi