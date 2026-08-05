#!/bin/bash
# Malaxis Fleet API End-to-End Test Suite
# Usage: ./scripts/test_api.sh [base_url] [admin_user] [admin_pass]
#   base_url defaults to http://localhost:8000
#   admin_user defaults to "admin"
#   admin_pass defaults to $ADMIN_PASS_ENV or the ADMIN_PASS value from .env

set -euo pipefail

BASE="${1:-http://localhost:8000}"
ADMIN_USER="${2:-admin}"
ADMIN_PASS="${3:-}"

# Read ADMIN_PASS from .env (without sourcing it, so embedded backticks/quotes
# in the value can never be executed). Applies only to the password variable.
if [ -z "$ADMIN_PASS" ] && [ -f .env ] && grep -q '^[[:space:]]*ADMIN_PASS=' .env; then
    ADMIN_PASS=$(grep '^[[:space:]]*ADMIN_PASS=' .env | head -1 | sed 's/^[[:space:]]*ADMIN_PASS=//; s/^"//; s/"$//')
fi
if [ -z "$ADMIN_PASS" ]; then
    echo "ERROR: No admin password supplied. Pass it as \$3 or set ADMIN_PASS in .env" >&2
    exit 1
fi

if ! command -v python3 &>/dev/null && ! command -v jq &>/dev/null; then
    echo "ERROR: This script requires python3 or jq for JSON handling."
    exit 1
fi

COOKIE_JAR=$(mktemp)
PASS=0
FAIL=0

cleanup() {
    rm -f "$COOKIE_JAR" /tmp/api_resp.json
}
trap cleanup EXIT

log()    { echo -e "  [TEST] $*"; }
pass()   { echo -e "  [PASS] $*"; PASS=$((PASS+1)); }
fail()   { echo -e "  [FAIL] $*"; FAIL=$((FAIL+1)); }
heading(){ echo -e "\n========================================"; echo "  $*"; echo "========================================"; }

json() {
    python3 -c "
import json, sys
obj = {}
for arg in sys.argv[1:]:
    k, sep, v = arg.partition('=')
    obj[k] = v
print(json.dumps(obj))
" "$@"
}

api() {
    local method="$1" path="$2" data="$3" expected="$4"
    if [ -n "$data" ]; then
        code=$(curl -s -o /tmp/api_resp.json -w "%{http_code}" -X "$method" \
            -b "$COOKIE_JAR" -c "$COOKIE_JAR" \
            -H "Content-Type: application/json" -d "$data" \
            "${BASE}${path}")
    else
        code=$(curl -s -o /tmp/api_resp.json -w "%{http_code}" -X "$method" \
            -b "$COOKIE_JAR" -c "$COOKIE_JAR" \
            "${BASE}${path}")
    fi
    if [ "$code" = "$expected" ]; then
        echo "$code"
        return 0
    fi
    echo "$code (expected $expected)"
    return 1
}

# ─── 1. Health Check ───
heading "1. Health Check"
code=$(curl -s -o /dev/null -w "%{http_code}" "${BASE}/api/health" 2>/dev/null || echo "000")
if [ "$code" = "200" ]; then
    pass "GET /api/health -> $code"
else
    fail "GET /api/health -> $code (expected 200)"
fi

# ─── 2. Login ───
heading "2. Login"
data=$(json "username=${ADMIN_USER}" "password=${ADMIN_PASS}")
code=$(api POST /api/auth/login "$data" 200) || true
if [ "$code" = "200" ]; then
    pass "POST /api/auth/login -> $code"
else
    fail "POST /api/auth/login -> $code (expected 200)"
fi

# ─── 3. Get Current User (me) ───
heading "3. Get Current User"
code=$(api GET /api/auth/me "" 200) || true
if [ "$code" = "200" ]; then
    pass "GET /api/auth/me -> $code"
    ROLE=$(python3 -c "import json;print(json.load(open('/tmp/api_resp.json')).get('role',''))" 2>/dev/null || \
           grep -o '"role":"[^"]*"' /tmp/api_resp.json 2>/dev/null | head -1 | sed 's/"role":"//;s/"//')
    echo "       Logged in as: ${ADMIN_USER} (role: ${ROLE})"
else
    fail "GET /api/auth/me -> $code (expected 200)"
fi

# ─── 4. List Roles ───
heading "4. List Roles"
code=$(api GET /api/web/roles "" 200) || true
if [ "$code" = "200" ]; then
    pass "GET /api/web/roles -> $code"
else
    fail "GET /api/web/roles -> $code (expected 200)"
fi

# ─── 5. Create Custom Role ───
heading "5. Create Custom Role"
ROLE_NAME="test-role-$(date +%s)"
data=$(json "name=${ROLE_NAME}" "color_hex=#FF5733" "permissions_json=[\"can_view_nodes\"]")
code=$(api POST /api/web/roles "$data" 201) || true
if [ "$code" = "201" ]; then
    pass "POST /api/web/roles -> $code (name: ${ROLE_NAME})"
    ROLE_ID=$(python3 -c "import json;print(json.load(open('/tmp/api_resp.json')).get('id',0))" 2>/dev/null || \
              grep -o '"id":[0-9]*' /tmp/api_resp.json 2>/dev/null | head -1 | sed 's/"id"://')
    echo "       Created role ID: ${ROLE_ID}"
else
    fail "POST /api/web/roles -> $code (expected 201)"
    # Try to get any existing role ID for update test
    ROLE_ID=""
fi

# ─── 6. Update Custom Role ───
heading "6. Update Custom Role"
if [ -n "$ROLE_ID" ] && [ "$ROLE_ID" != "0" ] && [ "$ROLE_ID" != "null" ]; then
    data=$(json "color_hex=#00FF00")
    code=$(api PUT "/api/web/roles/${ROLE_ID}" "$data" 200) || true
    if [ "$code" = "200" ]; then
        pass "PUT /api/web/roles/${ROLE_ID} -> $code"
    else
        fail "PUT /api/web/roles/${ROLE_ID} -> $code (expected 200)"
    fi
else
    fail "Skipped: no role ID available for update test"
fi

# ─── 7. List Users ───
heading "7. List Users"
code=$(api GET /api/web/users "" 200) || true
if [ "$code" = "200" ]; then
    pass "GET /api/web/users -> $code"
else
    fail "GET /api/web/users -> $code (expected 200)"
fi

# ─── 8. Create User ───
heading "8. Create User"
TEST_USER="testuser-$(date +%s)"
TEST_PASS="TestPass123!"
data=$(json "username=${TEST_USER}" "password=${TEST_PASS}" "role=client")
code=$(api POST /api/web/users "$data" 201) || true
if [ "$code" = "201" ]; then
    pass "POST /api/web/users -> $code (username: ${TEST_USER})"
    USER_ID=$(python3 -c "import json;print(json.load(open('/tmp/api_resp.json')).get('id',0))" 2>/dev/null || \
              grep -o '"id":[0-9]*' /tmp/api_resp.json 2>/dev/null | head -1 | sed 's/"id"://')
    echo "       Created user ID: ${USER_ID}"
else
    fail "POST /api/web/users -> $code (expected 201)"
    USER_ID=""
fi

# ─── 9. Update User ───
heading "9. Update User"
if [ -n "$USER_ID" ] && [ "$USER_ID" != "0" ] && [ "$USER_ID" != "null" ]; then
    data=$(json "role=client" "color_hex=#00AAFF")
    code=$(api PUT "/api/web/users/${USER_ID}" "$data" 200) || true
    if [ "$code" = "200" ]; then
        pass "PUT /api/web/users/${USER_ID} -> $code"
        UPDATED_ROLE=$(python3 -c "import json;print(json.load(open('/tmp/api_resp.json')).get('role',''))" 2>/dev/null || echo "")
        echo "       Updated user role: ${UPDATED_ROLE}"
    else
        fail "PUT /api/web/users/${USER_ID} -> $code (expected 200)"
        # Show error body
        cat /tmp/api_resp.json 2>/dev/null || true
    fi
else
    fail "Skipped: no user ID available for update test"
fi

# ─── 10. Update User Password ───
heading "10. Update User Password"
if [ -n "$USER_ID" ] && [ "$USER_ID" != "0" ] && [ "$USER_ID" != "null" ]; then
    data=$(json "password=NewPass456!")
    code=$(api PUT "/api/web/users/${USER_ID}" "$data" 200) || true
    if [ "$code" = "200" ]; then
        pass "PUT /api/web/users/${USER_ID} (password update) -> $code"
    else
        fail "PUT /api/web/users/${USER_ID} (password) -> $code (expected 200)"
    fi
else
    fail "Skipped: no user ID for password update test"
fi

# ─── 11. Delete Created User ───
heading "11. Delete Created User"
if [ -n "$USER_ID" ] && [ "$USER_ID" != "0" ] && [ "$USER_ID" != "null" ]; then
    code=$(api DELETE "/api/web/users/${USER_ID}" "" 204) || true
    if [ "$code" = "204" ]; then
        pass "DELETE /api/web/users/${USER_ID} -> $code"
    else
        fail "DELETE /api/web/users/${USER_ID} -> $code (expected 204)"
    fi
else
    fail "Skipped: no user ID for delete test"
fi

# ─── 12. Delete Created Role ───
heading "12. Delete Created Role"
if [ -n "$ROLE_ID" ] && [ "$ROLE_ID" != "0" ] && [ "$ROLE_ID" != "null" ]; then
    code=$(api DELETE "/api/web/roles/${ROLE_ID}" "" 204) || true
    if [ "$code" = "204" ]; then
        pass "DELETE /api/web/roles/${ROLE_ID} -> $code"
    else
        fail "DELETE /api/web/roles/${ROLE_ID} -> $code (expected 204)"
        cat /tmp/api_resp.json 2>/dev/null || true
    fi
else
    fail "Skipped: no role ID for delete test"
fi

# ─── 13. Get Audit Logs ───
heading "13. Get Audit Logs"
code=$(api GET /api/web/audit "" 200) || true
if [ "$code" = "200" ]; then
    pass "GET /api/web/audit -> $code"
else
    fail "GET /api/web/audit -> $code (expected 200)"
fi

# ─── 14. Get Settings ───
heading "14. Get Settings"
code=$(api GET /api/web/settings "" 200) || true
if [ "$code" = "200" ]; then
    pass "GET /api/web/settings -> $code"
else
    fail "GET /api/web/settings -> $code (expected 200)"
fi

# ─── Summary ───
heading "RESULTS"
TOTAL=$((PASS+FAIL))
echo "  Passed: ${PASS}/${TOTAL}"
echo "  Failed: ${FAIL}/${TOTAL}"
if [ "$FAIL" -gt 0 ]; then
    echo -e "\n  ❌ SOME TESTS FAILED"
    exit 1
else
    echo -e "\n  ✅ ALL TESTS PASSED"
    exit 0
fi
