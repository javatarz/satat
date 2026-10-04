#!/bin/bash
# Test script to verify editorconfig compliance for specific files

echo "Testing editorconfig compliance for critical files..."

# Test terraform files (T1 and T2)
echo "Checking Terraform files..."
pre-commit run editorconfig-checker --files terraform/* > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "✅ Terraform files are editorconfig compliant"
else
    echo "❌ Terraform files have editorconfig issues"
    pre-commit run editorconfig-checker --files terraform/*
fi

# Test gateway files (T3)
echo -e "\nChecking Gateway files..."
pre-commit run editorconfig-checker --files gateway/* > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "✅ Gateway files are editorconfig compliant"
else
    echo "❌ Gateway files have editorconfig issues"
fi

# Test automation files (T8)
echo -e "\nChecking Automation files..."
pre-commit run editorconfig-checker --files automations/* > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "✅ Automation files are editorconfig compliant"
else
    echo "❌ Automation files have editorconfig issues"
fi

echo -e "\nTest complete. For detailed information, run ./scripts/check-editorconfig.sh"