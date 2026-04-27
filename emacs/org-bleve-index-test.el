;;; org-bleve-index-test.el --- Tests for org-bleve-index -*- lexical-binding: t; -*-

(require 'ert)
(require 'cl-lib)
(require 'org)
(require 'org-bleve-index)

(defmacro org-bleve-index-test--with-clean-state (&rest body)
  `(let ((org-mode-hook org-mode-hook))
     (unwind-protect
         (progn
           (clrhash org-bleve-index--inflight-update-processes)
           (clrhash org-bleve-index--pending-update-requests)
           ,@body)
       (clrhash org-bleve-index--inflight-update-processes)
       (clrhash org-bleve-index--pending-update-requests)
       (org-bleve-index-mode -1))))

(ert-deftest org-bleve-index-command-args-use-json-and-config ()
  (let ((org-bleve-index-config-file "~/org-search/config.txtpb"))
    (should (equal (org-bleve-index--command-args "update-file" '("/tmp/file.org"))
                   (list "--json"
                         "--config"
                         (expand-file-name org-bleve-index-config-file)
                         "update-file"
                         "/tmp/file.org")))))

(ert-deftest org-bleve-index-command-args-omit-config-when-unset ()
  (let ((org-bleve-index-config-file nil))
    (should (equal (org-bleve-index--command-args "rebuild" nil)
                   '("--json" "rebuild")))))

(ert-deftest org-bleve-index-update-buffer-starts-update-command ()
  (with-temp-buffer
    (org-mode)
    (setq-local buffer-file-name "/tmp/test.org")
    (cl-letf (((symbol-function 'org-bleve-index--request-update)
               (lambda (path silent-success)
                 (should (equal path "/tmp/test.org"))
                 (should-not silent-success)
                 'process)))
      (should (eq (org-bleve-index-update-buffer) 'process)))))

(ert-deftest org-bleve-index-update-buffer-requires-file-backed-org-buffer ()
  (with-temp-buffer
    (org-mode)
    (should-error (org-bleve-index-update-buffer) :type 'user-error))
  (with-temp-buffer
    (setq-local buffer-file-name "/tmp/test.txt")
    (should-error (org-bleve-index-update-buffer) :type 'user-error)))

(ert-deftest org-bleve-index-rebuild-starts-rebuild-command ()
  (cl-letf (((symbol-function 'org-bleve-index--start-command)
             (lambda (subcommand arguments silent-success)
               (should (equal subcommand "rebuild"))
               (should-not arguments)
               (should-not silent-success)
               'process)))
    (should (eq (org-bleve-index-rebuild) 'process))))

(ert-deftest org-bleve-index-mode-installs-buffer-local-after-save-hooks ()
  (org-bleve-index-test--with-clean-state
   (with-temp-buffer
     (org-mode)
     (org-bleve-index-mode 1)
     (should (memq #'org-bleve-index--enable-current-buffer org-mode-hook))
     (should (memq #'org-bleve-index--after-save after-save-hook))
     (org-bleve-index-mode -1)
     (should-not (memq #'org-bleve-index--enable-current-buffer org-mode-hook))
     (should-not (memq #'org-bleve-index--after-save after-save-hook)))))

(ert-deftest org-bleve-index-mode-installs-hook-for-future-org-buffers ()
  (org-bleve-index-test--with-clean-state
   (org-bleve-index-mode 1)
   (with-temp-buffer
     (org-mode)
     (should (memq #'org-bleve-index--after-save after-save-hook)))))

(ert-deftest org-bleve-index-after-save-starts-update-for-org-file-buffers ()
  (with-temp-buffer
    (org-mode)
    (setq-local buffer-file-name "/tmp/test.org")
    (let ((org-bleve-index-mode t)
          (org-bleve-index-auto-sync t))
      (cl-letf (((symbol-function 'org-bleve-index--request-update)
                 (lambda (path silent-success)
                   (should (equal path "/tmp/test.org"))
                   (should silent-success)
                   'started)))
        (should (eq (org-bleve-index--after-save) 'started))))))

(ert-deftest org-bleve-index-after-save-ignores-buffers-without-file-or-disabled-sync ()
  (with-temp-buffer
    (org-mode)
    (let ((org-bleve-index-mode t)
          (org-bleve-index-auto-sync t)
          (called nil))
      (cl-letf (((symbol-function 'org-bleve-index--request-update)
                 (lambda (_path _silent-success)
                   (setq called t))))
        (org-bleve-index--after-save)
        (should-not called))))
  (with-temp-buffer
    (org-mode)
    (setq-local buffer-file-name "/tmp/test.org")
    (let ((org-bleve-index-mode t)
          (org-bleve-index-auto-sync nil)
          (called nil))
      (cl-letf (((symbol-function 'org-bleve-index--request-update)
                 (lambda (_path _silent-success)
                   (setq called t))))
        (org-bleve-index--after-save)
        (should-not called)))))

(ert-deftest org-bleve-index-request-update-starts-immediately-without-inflight-process ()
  (org-bleve-index-test--with-clean-state
   (cl-letf (((symbol-function 'org-bleve-index--start-update-process)
              (lambda (path silent-success)
                (should (equal path "/tmp/test.org"))
                (should silent-success)
                'process)))
     (should (eq (org-bleve-index--request-update "/tmp/test.org" t) 'process)))))

(ert-deftest org-bleve-index-request-update-queues-when-process-is-inflight ()
  (org-bleve-index-test--with-clean-state
   (puthash "/tmp/test.org" 'running org-bleve-index--inflight-update-processes)
   (cl-letf (((symbol-function 'org-bleve-index--start-update-process)
              (lambda (_path _silent-success)
                (ert-fail "should not start a second concurrent update process"))))
     (should (eq (org-bleve-index--request-update "/tmp/test.org" t) 'queued))
     (should (equal (gethash "/tmp/test.org" org-bleve-index--pending-update-requests)
                    '(:silent-success t))))))

(ert-deftest org-bleve-index-request-update-coalesces-queued-reruns-per-file-path ()
  (org-bleve-index-test--with-clean-state
   (puthash "/tmp/test.org" 'running org-bleve-index--inflight-update-processes)
   (org-bleve-index--request-update "/tmp/test.org" t)
   (org-bleve-index--request-update "/tmp/test.org" nil)
   (should (equal (gethash "/tmp/test.org" org-bleve-index--pending-update-requests)
                  '(:silent-success nil)))))

(ert-deftest org-bleve-index-drain-pending-update-starts-one-rerun-and-clears-queue ()
  (org-bleve-index-test--with-clean-state
   (puthash "/tmp/test.org" '(:silent-success nil) org-bleve-index--pending-update-requests)
   (cl-letf (((symbol-function 'org-bleve-index--start-update-process)
              (lambda (path silent-success)
                (should (equal path "/tmp/test.org"))
                (should-not silent-success)
                'rerun-process)))
     (should (eq (org-bleve-index--drain-pending-update "/tmp/test.org") 'rerun-process))
     (should-not (gethash "/tmp/test.org" org-bleve-index--pending-update-requests)))))

;;; org-bleve-index-test.el ends here
