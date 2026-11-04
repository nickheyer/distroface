#!/bin/bash
# Discovery edge-case probe for T2 (registry+GC). Scratch script, not a product artifact.
# Drives the docker-registry protocol edge cases listed in the ticket against a live server.
set -u
ADDR=127.0.0.1:8080
USER=loadadmin
PASS=loadpass123
REPO=loadadmin/edge-repo
BASE=http://$ADDR

mint() { # mint <repo> <actions>
  curl -s -u $USER:$PASS "$BASE/auth/token?service=distroface-registry&scope=repository:$1:$2" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))'
}
hdr() { echo "Authorization: Bearer $1"; }
section() { echo; echo "=== $1 ==="; }

TOK=$(mint $REPO "pull,push")
AH=$(hdr $TOK)

# seed a blob + manifest
CFG='{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}'
python3 -c "import os;open('/tmp/t2layer','wb').write(os.urandom(64*1024))"
LDGST=sha256:$(python3 -c "import hashlib;print(hashlib.sha256(open('/tmp/t2layer','rb').read()).hexdigest())")
CGST=sha256:$(printf '%s' "$CFG" | shasum -a 256 | cut -d' ' -f1)

section "seed blob (monolithic PUT)"
LOC=$(curl -s -o /dev/null -D - -X POST "$BASE/v2/$REPO/blobs/uploads/" -H "$AH" | tr -d '\r' | awk -F': ' 'tolower($1)=="location"{print $2}')
SEP='&'; case "$LOC" in *\?*) ;; *) SEP='?';; esac
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$BASE/v2/$REPO/blobs/uploads/${LOC#*/v2/$REPO/blobs/uploads/}${SEP}digest=$LDGST" -H "$AH" -H 'Content-Type: application/octet-stream' --data-binary @/tmp/t2layer)
echo "PUT blob status=$CODE"

CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/v2/$REPO/blobs/uploads/?mount=$LDGST&from=$REPO" -H "$AH")
echo "self-mount status=$CODE"

section "1. HEAD then GET blob"
CODE=$(curl -s -o /dev/null -w '%{http_code} len=%{size_download}' -I "$BASE/v2/$REPO/blobs/$LDGST" -H "$AH")
echo "HEAD blob: $CODE"
CODE=$(curl -s -o /dev/null -w '%{http_code} len=%{size_download}' "$BASE/v2/$REPO/blobs/$LDGST" -H "$AH")
echo "GET blob:  $CODE (expect len=65536)"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v2/$REPO/blobs/$LDGST" -H "Range: bytes=0-99" -H "$AH")
echo "GET blob Range bytes=0-99: $CODE len=$(curl -s -o /dev/null -w '%{size_download}' -r 0-99 "$BASE/v2/$REPO/blobs/$LDGST" -H "$AH")"

section "2. chunked (PATCH) upload"
LOC2=$(curl -s -o /dev/null -D - -X POST "$BASE/v2/$REPO/blobs/uploads/" -H "$AH" | tr -d '\r' | awk -F': ' 'tolower($1)=="location"{print $2}')
U2="${LOC2#*/v2/$REPO/blobs/uploads/}"
CH=$(head -c 32768 /tmp/t2layer | tail -c 32768)
# first 32KiB chunk
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PATCH "$BASE/v2/$REPO/blobs/uploads/$U2" -H "$AH" -H 'Content-Type: application/octet-stream' -H 'Content-Range: 0-32767' -H 'Content-Length: 32768' --data-binary @<(head -c 32768 /tmp/t2layer))
echo "PATCH chunk1 status=$CODE"
# upload status probe
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v2/$REPO/blobs/uploads/$U2" -H "$AH")
echo "GET upload status=$CODE (expect 204)"
# second chunk + finalize
python3 - <<'PYEOF'
data = open('/tmp/t2layer','rb').read()
open('/tmp/t2chunk2','wb').write(data[32768:])
PYEOF
SEP='&'; case "$U2" in *\?*) ;; *) SEP='?';; esac
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$BASE/v2/$REPO/blobs/uploads/$U2${SEP}digest=$LDGST" -H "$AH" -H 'Content-Type: application/octet-stream' -H 'Content-Length: 32768' --data-binary @/tmp/t2chunk2)
echo "PUT final chunk status=$CODE"

section "3. schema2 manifest push/pull, then schema1"
MDGST=$(python3 - <<'PYEOF'
import json, hashlib
layer = open('/tmp/t2layer','rb').read()
cfg = open('/tmp/t2cfg','wb')
cfg.write(b'{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}')
cfg.close()
cfgb = open('/tmp/t2cfg','rb').read()
cd = 'sha256:'+hashlib.sha256(cfgb).hexdigest()
ld = 'sha256:'+hashlib.sha256(layer).hexdigest()
m = {"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",
     "config":{"mediaType":"application/vnd.docker.container.image.v1+json","size":len(cfgb),"digest":cd},
     "layers":[{"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip","size":len(layer),"digest":ld}]}
open('/tmp/t2man','wb').write(json.dumps(m).encode())
open('/tmp/t2man_digest','w').write('sha256:'+hashlib.sha256(json.dumps(m).encode()).hexdigest())
print('sha256:'+hashlib.sha256(json.dumps(m).encode()).hexdigest())
PYEOF
)
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$BASE/v2/$REPO/manifests/t2tag" -H "$AH" -H 'Content-Type: application/vnd.docker.distribution.manifest.v2+json' --data-binary @/tmp/t2man)
echo "PUT schema2 manifest=$CODE"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v2/$REPO/manifests/t2tag" -H "$AH" -H 'Accept: application/vnd.docker.distribution.manifest.v2+json')
echo "GET schema2 manifest=$CODE"
# HEAD manifest (docker does this first)
CODE=$(curl -s -o /dev/null -w '%{http_code} dgst=%header{Docker-Content-Digest}' -I "$BASE/v2/$REPO/manifests/t2tag" -H "$AH" -H 'Accept: application/vnd.docker.distribution.manifest.v2+json')
echo "HEAD schema2 manifest=$CODE"

# schema1
python3 - <<'PYEOF'
import json, hashlib
cfgb = open('/tmp/t2cfg','rb').read()
layer = open('/tmp/t2layer','rb').read()
cd = 'sha256:'+hashlib.sha256(cfgb).hexdigest()
ld = 'sha256:'+hashlib.sha256(layer).hexdigest()
fsl = {"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip","size":len(layer),"digest":ld}
h = {"created":"2020-01-01T00:00:00Z","container_config":{"Cmd":[]}}
hist = [{"v1Compatibility":json.dumps({**h,"id":cd[:12]})},{"v1Compatibility":json.dumps({**h,"id":"0"*12,"parent":cd[:12]})}]
s1 = {"schemaVersion":1,"name":"loadadmin/edge-repo","tag":"t2tag","architecture":"amd64","fsLayers":[fsl],"history":hist}
open('/tmp/t2s1','wb').write(json.dumps(s1).encode())
print('written')
PYEOF
CODE=$(curl -s -o /tmp/t2s1resp -w '%{http_code}' -X PUT "$BASE/v2/$REPO/manifests/t2s1" -H "$AH" -H 'Content-Type: application/vnd.docker.distribution.manifest.v1+prettyjws' --data-binary @/tmp/t2s1)
echo "PUT schema1 manifest=$CODE body=$(head -c 200 /tmp/t2s1resp)"

section "4. OCI index + manifest list"
python3 - <<'PYEOF'
import json
idx = {"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json",
       "manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":open('/tmp/t2man_digest').read().strip(),"size":123,"platform":{"architecture":"amd64","os":"linux"}}]}
open('/tmp/t2idx','wb').write(json.dumps(idx).encode())
PYEOF
CODE=$(curl -s -o /tmp/t2idxresp -w '%{http_code}' -X PUT "$BASE/v2/$REPO/manifests/t2idx" -H "$AH" -H 'Content-Type: application/vnd.oci.image.index.v1+json' --data-binary @/tmp/t2idx)
echo "PUT OCI index=$CODE body=$(head -c 200 /tmp/t2idxresp)"

section "5. tag listing + pagination"
CODE=$(curl -s "$BASE/v2/$REPO/tags/list" -H "$AH" | head -c 300); echo "tags/list: $CODE"
CODE=$(curl -s "$BASE/v2/$REPO/tags/list?n=1" -H "$AH" | head -c 300); echo "tags/list n=1: $CODE"
LINK=$(curl -s -D - -o /dev/null "$BASE/v2/$REPO/tags/list?n=1" -H "$AH" | tr -d '\r' | awk -F': ' 'tolower($1)=="link"{print $2}')
echo "Link header: ${LINK:-<none>}"

section "6. catalog"
CODE=$(curl -s "$BASE/v2/_catalog?n=100" -H "$AH" | head -c 300); echo "catalog: $CODE"

section "7. 401 challenge flow"
OUT=$(curl -s -D - -o /dev/null "$BASE/v2/$REPO/manifests/t2tag" 2>/dev/null | tr -d '\r')
echo "$OUT" | grep -E "^(HTTP|WWW-Authenticate)" | head -4

section "8. cross-repo blob mount"
TOK2=$(mint edge/other "pull,push")
AH2=$(hdr $TOK2)
CODE=$(curl -s -o /dev/null -w '%{http_code} loc=%header{Location}' -X POST "$BASE/v2/edge/other/blobs/uploads/?mount=$LDGST&from=$REPO" -H "$AH2")
echo "cross-repo mount (same user): $CODE"
# mount with a token that lacks from-repo access
TOK3=$(mint edge/other2 "pull,push")
AH3=$(hdr $TOK3)
CODE=$(curl -s -o /dev/null -w '%{http_code} loc=%header{Location}' -X POST "$BASE/v2/edge/other2/blobs/uploads/?mount=$LDGST&from=$REPO" -H "$AH3")
echo "cross-repo mount (no source perms): $CODE"

section "9. missing-layer manifest"
python3 - <<'PYEOF'
import json, hashlib
m = {"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",
     "config":{"mediaType":"application/vnd.docker.container.image.v1+json","size":100,"digest":"sha256:"+("ab"*32)},
     "layers":[{"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip","size":100,"digest":"sha256:"+("cd"*32)}]}
open('/tmp/t2miss','wb').write(json.dumps(m).encode())
PYEOF
CODE=$(curl -s -o /tmp/t2missresp -w '%{http_code}' -X PUT "$BASE/v2/$REPO/manifests/t2miss" -H "$AH" -H 'Content-Type: application/vnd.docker.distribution.manifest.v2+json' --data-binary @/tmp/t2miss)
echo "PUT manifest w/ missing layers=$CODE body=$(head -c 200 /tmp/t2missresp)"
CODE=$(curl -s -o /tmp/t2missget -w '%{http_code}' "$BASE/v2/$REPO/manifests/t2miss" -H "$AH" -H 'Accept: application/vnd.docker.distribution.manifest.v2+json')
echo "GET manifest w/ missing layers=$CODE body=$(head -c 150 /tmp/t2missget)"

section "10. unknown blob/manifest"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v2/$REPO/blobs/sha256:$(python3 -c "print('e'*64)")" -H "$AH")
echo "GET unknown blob=$CODE (expect 404)"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v2/$REPO/manifests/nosuchtag" -H "$AH")
echo "GET unknown tag=$CODE (expect 404)"

section "11. invalid digest formats"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v2/$REPO/blobs/sha256:tooshort" -H "$AH")
echo "GET blob invalid digest=$CODE"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v2/$REPO/blobs/notadigest" -H "$AH")
echo "GET blob non-digest name=$CODE"

section "12. delete manifest + blob"
MDGST2=$(cat /tmp/t2man_digest)
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$BASE/v2/$REPO/manifests/$MDGST2" -H "$AH")
echo "DELETE manifest by digest=$CODE"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$BASE/v2/$REPO/blobs/$LDGST" -H "$AH")
echo "DELETE blob=$CODE"
echo; echo "EDGE-CASE PROBE COMPLETE"
