package services

// s3_append_e2e_test.go — REAL end-to-end proof for the appendable-object
// BadDigest incident (OSS AppendObject binlogs → MinIO all failing with
// "api error BadDigest: The Content-Md5 you specified did not match what
// we received").
//
// Topology:
//
//	fake s3 source (httptest)      real rclone rcd           real MinIO
//	LIST ETag ≠ content MD5   →    sync/copy submitted by    →   PutObject
//	(AppendObject semantics)       clonectl RunTask+PollRun      digest check
//
// The only test double is the SOURCE's reported ETag — exactly the one
// property that makes appendable objects special (their ETag is not the
// content MD5, yet it looks like one: 32 hex chars, no "-N" suffix).
// Everything downstream is real: the option pipeline (splitRcloneOptions →
// _config/_filter + fs spec backend overrides), the real rclone binary, its
// Content-MD5 header on PutObject and MinIO's server-side digest validation.
// If clonectl routed upload_cutoff anywhere but the fs spec override (the
// only place s3 backend options actually take effect), rclone would silently
// drop it and scenario 3 would fail with BadDigest — which is precisely what
// this test exists to catch.
//
// Scenario matrix:
//
//	1. {}                              → binlog fails with BadDigest;
//	                                     static full-backup succeeds (control)
//	2. {disable_checksum}              → STILL BadDigest: on rclone v1.75
//	                                     single-part PutObject always sends
//	                                     Content-Md5 (verified in s3.go: the
//	                                     option only gates metadata md5);
//	                                     documented here as a dead end
//	3. {upload_cutoff=0,ignore_checksum} → success; MinIO bytes == source
//	                                     bytes (multipart part digests are
//	                                     computed from the bytes sent)
//	4. {+min_age}                      → still-appending object skipped,
//	                                     frozen binlog synced
//
// Guards: skips unless a real rclone binary is available (RCLONE_BIN or PATH)
// AND MinIO is reachable via MINIO_ENDPOINT/MINIO_ACCESS_KEY/MINIO_SECRET_KEY
// (scripts/s3_append_e2e.sh provisions both, incl. the MinIO container).

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rclone_sync/internal/database"
	"rclone_sync/internal/rclone"
)

// --- fake s3 source -------------------------------------------------------

type fakeS3Object struct {
	data    []byte
	etag    string // what LIST/GET report; deliberately ≠ content digest for appendables
	modTime time.Time
}

// fakeS3Source serves one bucket over the S3 REST surface that rclone's s3
// backend actually exercises: ListObjects (v1 + v2), object GET/HEAD, bucket
// HEAD, ?location and ?uploads subresources. Auth headers are ignored.
type fakeS3Source struct {
	srv     *httptest.Server
	bucket  string
	mu      sync.Mutex
	objects map[string]*fakeS3Object
	test    *testing.T // set post-construction; enables request logging
}

func newFakeS3Source(t *testing.T, bucket string) *fakeS3Source {
	t.Helper()
	f := &fakeS3Source{bucket: bucket, objects: map[string]*fakeS3Object{}, test: t}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

// put adds one object; etag is whatever the server should REPORT (bogus for
// appendable semantics, content-true for honest objects).
func (f *fakeS3Source) put(key string, data []byte, etag string, modTime time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := append([]byte(nil), data...)
	f.objects[key] = &fakeS3Object{data: cp, etag: etag, modTime: modTime}
}

func (f *fakeS3Source) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t := f.test
	if t != nil {
		t.Logf("[fake-s3] %s %s", r.Method, r.URL.RequestURI())
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p != f.bucket && !strings.HasPrefix(p, f.bucket+"/") {
		http.Error(w, "unknown path", http.StatusNotFound)
		return
	}
	key := strings.TrimPrefix(strings.TrimPrefix(p, f.bucket), "/")
	q := r.URL.Query()

	if key == "" {
		switch r.Method {
		case http.MethodHead: // bucket exists
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			switch {
			case q.Has("location"):
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`)
			case q.Has("uploads"):
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>%s</Bucket><KeyMarker></KeyMarker><UploadIdMarker></UploadIdMarker><MaxUploads>1000</MaxUploads><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`, f.bucket)
			default:
				f.list(w, q.Has("list-type"))
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	f.mu.Lock()
	obj := f.objects[key]
	f.mu.Unlock()
	if obj == nil {
		http.Error(w, "NoSuchKey", http.StatusNotFound)
		return
	}
	w.Header().Set("ETag", `"`+obj.etag+`"`)
	w.Header().Set("Last-Modified", obj.modTime.UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
	switch r.Method {
	case http.MethodHead:
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		_, _ = w.Write(obj.data)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeS3Source) list(w http.ResponseWriter, v2 bool) {
	f.mu.Lock()
	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	fmt.Fprintf(&b, "<Name>%s</Name><Prefix></Prefix><Marker></Marker><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>", f.bucket)
	if v2 {
		fmt.Fprintf(&b, "<KeyCount>%d</KeyCount>", len(keys))
	}
	for _, k := range keys {
		o := f.objects[k]
		// ETag is quoted; &#34; keeps strict XML parsers happy.
		fmt.Fprintf(&b,
			"<Contents><Key>%s</Key><LastModified>%s</LastModified><ETag>&#34;%s&#34;</ETag><Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents>",
			k, o.modTime.UTC().Format("2006-01-02T15:04:05.000Z"), o.etag, len(o.data))
	}
	b.WriteString(`</ListBucketResult>`)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(b.String()))
}

// --- infrastructure helpers ----------------------------------------------

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// startRcd boots a REAL rclone rcd via clonectl's manager on a free port.
// The test process chdirs into a temp dir first so rcd.log/rcd.pid land there.
func startRcd(t *testing.T, bin string) (m *rclone.Manager, client *rclone.Client, rcUser, rcPass string) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	tmp := t.TempDir()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	rcUser, rcPass = "rc-e2e", randHex(t, 12)
	m = rclone.NewManager(addr, rcUser, rcPass, bin)
	require.NoError(t, m.Start(45*time.Second), "real rclone rcd failed to start")
	t.Cleanup(m.Stop)
	return m, rclone.NewClient(m.RCURL, rcUser, rcPass, time.Minute), rcUser, rcPass
}

// rcPost is a direct rcd call for test-side plumbing (mkdir / list /
// copyfile) that the production client doesn't expose.
func rcPost(t *testing.T, baseURL, user, pass, path string, payload map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(user, pass)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "POST %s failed: %s", path, raw)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), "POST %s body: %s", path, raw)
	return out
}

func e2eListNames(t *testing.T, m *rclone.Manager, rcUser, rcPass, fs string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, e := range e2eListEntries(t, m, rcUser, rcPass, fs) {
		if n, ok := e["Name"].(string); ok {
			names[n] = true
		}
	}
	return names
}

func e2eListEntries(t *testing.T, m *rclone.Manager, rcUser, rcPass, fs string) []map[string]any {
	t.Helper()
	out := rcPost(t, m.RCURL, rcUser, rcPass, "/operations/list", map[string]any{"fs": fs, "remote": ""})
	lst, _ := out["list"].([]any)
	entries := make([]map[string]any, 0, len(lst))
	for _, e := range lst {
		if em, ok := e.(map[string]any); ok {
			entries = append(entries, em)
		}
	}
	return entries
}

// --- the test ---------------------------------------------------------------

func TestS3AppendSyncE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: real-rclone e2e skipped")
	}
	bin := os.Getenv("RCLONE_BIN")
	if bin == "" {
		if p, err := exec.LookPath("rclone"); err == nil {
			bin = p
		}
	}
	if bin == "" {
		t.Skip("no RCLONE_BIN and no rclone in PATH — run scripts/s3_append_e2e.sh")
	}
	minioEndpoint := os.Getenv("MINIO_ENDPOINT")
	minioAK := os.Getenv("MINIO_ACCESS_KEY")
	minioSK := os.Getenv("MINIO_SECRET_KEY")
	if minioEndpoint == "" || minioAK == "" || minioSK == "" {
		t.Skip("MINIO_ENDPOINT / MINIO_ACCESS_KEY / MINIO_SECRET_KEY unset — run scripts/s3_append_e2e.sh")
	}

	// -- real rcd -----------------------------------------------------------
	m, client, rcUser, rcPass := startRcd(t, bin)

	// -- minio direct remote (test-side plumbing) + bucket -------------------
	const dstBucket = "e2e-dst"
	_, err := client.CreateRemote("e2eminio", "s3", map[string]any{
		"provider": "Minio", "endpoint": "http://" + minioEndpoint,
		"access_key_id": minioAK, "secret_access_key": minioSK,
		"region": "us-east-1", "force_path_style": true,
	})
	require.NoError(t, err)
	rcPost(t, m.RCURL, rcUser, rcPass, "/operations/mkdir",
		map[string]any{"fs": "e2eminio:" + dstBucket, "remote": ""})

	// -- fake appendable source --------------------------------------------
	const srcBucket = "append-src"
	src := newFakeS3Source(t, srcBucket)

	// binlog: AppendObject semantics — reported ETag is NOT the content hash
	// but still looks like one (32 hex, no -N suffix). ~4 MiB keeps rclone on
	// the single-part PutObject path (upload cutoff default 200 MiB).
	binlogData := make([]byte, 4<<20)
	for i := range binlogData {
		binlogData[i] = byte(i * 7)
	}
	frozen := time.Now().Add(-time.Hour)
	src.put("binlog.000001", binlogData, randHex(t, 16), frozen) // bogus etag on purpose

	// control object: content-true ETag via the classic MD5 test vector,
	// mirroring the "full backup syncs fine" half of the incident. If the
	// vector were wrong the s1 control assertion would fail loudly — the
	// test stays self-verifying.
	fullData := []byte("The quick brown fox jumps over the lazy dog")
	src.put("full-backup.gz", fullData, "9e107d9d372bb6826bd81d3542a419d6", frozen)

	// -- db fixture: two s3 storage sources + data sources -------------------
	db, err := database.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })

	admin := database.User{Username: "root", PasswordHash: "x", Role: database.RoleAdmin}
	require.NoError(t, db.Create(&admin).Error)

	fakeURL := src.srv.URL
	srcStorage := database.StorageSource{Name: "e2e-append-src", Type: "s3",
		Endpoint: &fakeURL, Region: strPtr("us-east-1"),
		Extra: database.JSONObject{"provider": "Other", "force_path_style": true, "list_version": "1"}}
	require.NoError(t, db.Create(&srcStorage).Error)
	dstStorage := database.StorageSource{Name: "e2e-minio", Type: "s3",
		Endpoint: strPtr("http://" + minioEndpoint), Region: strPtr("us-east-1"),
		Extra: database.JSONObject{"provider": "Minio", "force_path_style": true}}
	require.NoError(t, db.Create(&dstStorage).Error)

	fakeAK, fakeSK := "fake-ak", "fake-sk" // fake source ignores credentials
	dsSrc := database.DataSource{Name: "append-src", StorageSourceID: srcStorage.ID,
		Path: "/" + srcBucket, AccessKeyID: &fakeAK, SecretAccessKey: &fakeSK, OwnerUserID: admin.ID}
	dsDst := database.DataSource{Name: "minio-dst", StorageSourceID: dstStorage.ID,
		Path: "/" + dstBucket, AccessKeyID: &minioAK, SecretAccessKey: &minioSK, OwnerUserID: admin.ID}
	require.NoError(t, db.Create(&dsSrc).Error)
	require.NoError(t, db.Create(&dsDst).Error)

	recipe := func(extra database.JSONObject) database.JSONObject {
		// upload_cutoff=0 forces multipart for every object: rclone computes
		// each part's Content-Md5 from the bytes actually sent (self-
		// consistent), so the bogus source ETag never reaches the wire as a
		// digest (rclone docs recommend exactly this when the source lacks
		// usable MD5 hashes). ignore_checksum skips the post-copy src/dst
		// hash compare, which can never match on appendable sources.
		o := database.JSONObject{"upload_cutoff": "0", "ignore_checksum": true}
		for k, v := range extra {
			o[k] = v
		}
		return o
	}
	mkTask := func(name, dstPrefix string, opts database.JSONObject) int64 {
		task := database.SyncTask{Name: name, SrcDataSourceID: dsSrc.ID, DstDataSourceID: dsDst.ID,
			SrcPath: "", DstPath: dstPrefix, Mode: database.ModeCopy,
			Cron: "0 3 * * *", Enabled: true, RcloneOptions: opts}
		require.NoError(t, db.Create(&task).Error)
		return task.ID
	}

	waitRun := func(taskID int64) *database.SyncRun {
		run, err := RunTask(db, client, "nodeA", 3600, taskID, database.TriggerManual)
		require.NoError(t, err)
		require.Equal(t, database.RunRunning, run.Status)
		deadline := time.Now().Add(2 * time.Minute)
		for {
			r, err := PollRun(db, client, run.ID)
			require.NoError(t, err)
			if r.Status != database.RunPending && r.Status != database.RunRunning {
				if os.Getenv("E2E_DUMP_RCD") != "" {
					if logTail, err := os.ReadFile("rcd.log"); err == nil {
						t.Logf("rcd.log after job %d:\n%s", *run.JobID, logTail)
					}
				}
				return r
			}
			if time.Now().After(deadline) {
				st, _ := client.JobStatus(*run.JobID)
				logTail, _ := os.ReadFile("rcd.log")
				t.Fatalf("sync job %d did not finish in time\nrc job status: %+v\nrcd.log tail:\n%s",
					*run.JobID, st, string(logTail))
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	destFS := fmt.Sprintf("e2eminio:%s", dstBucket)
	destHas := func(prefix, name string) bool {
		return e2eListNames(t, m, rcUser, rcPass, destFS+"/"+prefix)[name]
	}

	// -- scenario 1: no options → production symptom must reproduce ---------
	t.Run("s1_no_options_baddigest", func(t *testing.T) {
		run := waitRun(mkTask("s1", "s1", database.JSONObject{}))
		require.Equal(t, database.RunFailed, run.Status)
		require.NotNil(t, run.Error)
		assert.Contains(t, *run.Error, "BadDigest",
			"appendable object must hit MinIO's Content-MD5 rejection: %s", *run.Error)
		// control: the honest-ETag object synced fine despite the failed job
		assert.True(t, destHas("s1", "full-backup.gz"), "static object must sync")
		assert.False(t, destHas("s1", "binlog.000001"), "binlog must not land")
	})

	// -- scenario 2: disable_checksum is a dead end on rclone v1.75 ---------
	t.Run("s2_disable_checksum_still_baddigest", func(t *testing.T) {
		run := waitRun(mkTask("s2", "s2", database.JSONObject{"disable_checksum": true}))
		require.Equal(t, database.RunFailed, run.Status)
		require.NotNil(t, run.Error)
		// v1.75 s3.go: single-part PutObject sets Content-MD5 from src.Hash
		// unconditionally; the option only gates metadata md5 on multipart.
		assert.Contains(t, *run.Error, "BadDigest",
			"documents the v1.75 behavior: disable_checksum does not stop the Content-Md5 header: %s", *run.Error)
		assert.False(t, destHas("s2", "binlog.000001"))
	})

	// -- scenario 3: multipart recipe → success with byte-identical content --
	t.Run("s3_multipart_recipe_success", func(t *testing.T) {
		run := waitRun(mkTask("s3", "s3", recipe(nil)))
		require.Equal(t, database.RunSuccess, run.Status)
		if run.Error != nil {
			t.Logf("run.Error: %s", *run.Error)
		}
		require.True(t, destHas("s3", "binlog.000001"))

		// byte-for-byte proof: pull the object back out and compare. A plain
		// operations/copyfile would REFUSE the transfer — the multipart ETag
		// reported by the S3 server is not the content digest — so pull with
		// ignore_checksum and compare bytes ourselves (the real proof).
		tmp := t.TempDir()
		jobID, err := client.StartSync(destFS+"/s3", tmp, "copy",
			database.JSONObject{"ignore_checksum": true})
		require.NoError(t, err)
		deadline := time.Now().Add(time.Minute)
		for {
			st, err := client.JobStatus(jobID)
			require.NoError(t, err)
			if finished, _ := st["finished"].(bool); finished {
				require.Equal(t, true, st["success"], "pull-back job failed: %v", st["error"])
				break
			}
			require.Less(t, time.Now(), deadline, "pull-back job did not finish in time")
			time.Sleep(200 * time.Millisecond)
		}
		got, err := os.ReadFile(tmp + "/binlog.000001")
		require.NoError(t, err)
		assert.True(t, bytes.Equal(binlogData, got),
			"object in MinIO must be byte-identical to the source content")
	})

	// -- scenario 4: min_age skips the still-appending object ----------------
	t.Run("s4_min_age_freeze_window", func(t *testing.T) {
		src.put("binlog.000002", []byte("still being appended"), randHex(t, 16), time.Now())
		run := waitRun(mkTask("s4", "s4", recipe(database.JSONObject{"min_age": "5m"})))
		require.Equal(t, database.RunSuccess, run.Status)
		if run.Error != nil {
			t.Logf("run.Error: %s", *run.Error)
		}
		assert.True(t, destHas("s4", "binlog.000001"), "frozen binlog must sync")
		assert.False(t, destHas("s4", "binlog.000002"), "fresh binlog must be skipped by min_age")
	})
}
