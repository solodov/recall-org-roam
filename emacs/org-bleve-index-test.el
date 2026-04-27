;;; org-bleve-index-test.el --- Tests for org-bleve-index -*- lexical-binding: t; -*-

(require 'ert)
(require 'cl-lib)
(require 'org)
(require 'org-bleve-index)

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
    (cl-letf (((symbol-function 'org-bleve-index--start-command)
               (lambda (subcommand arguments silent-success)
                 (should (equal subcommand "update-file"))
                 (should (equal arguments '("/tmp/test.org")))
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

(ert-deftest org-bleve-index-mode-adds-and-removes-save-hook ()
  (let ((after-save-hook after-save-hook))
    (unwind-protect
        (progn
          (org-bleve-index-mode 1)
          (should (memq #'org-bleve-index--after-save after-save-hook))
          (org-bleve-index-mode -1)
          (should-not (memq #'org-bleve-index--after-save after-save-hook)))
      (org-bleve-index-mode -1))))

(ert-deftest org-bleve-index-after-save-starts-update-for-org-file-buffers ()
  (with-temp-buffer
    (org-mode)
    (setq-local buffer-file-name "/tmp/test.org")
    (let ((org-bleve-index-mode t)
          (org-bleve-index-auto-sync t))
      (cl-letf (((symbol-function 'org-bleve-index-update-buffer)
                 (lambda (silent-success)
                   (should silent-success)
                   'started)))
        (should (eq (org-bleve-index--after-save) 'started))))))

(ert-deftest org-bleve-index-after-save-ignores-non-org-buffers-and-disabled-sync ()
  (with-temp-buffer
    (setq-local buffer-file-name "/tmp/test.txt")
    (let ((org-bleve-index-mode t)
          (org-bleve-index-auto-sync t)
          (called nil))
      (cl-letf (((symbol-function 'org-bleve-index-update-buffer)
                 (lambda (_silent-success)
                   (setq called t))))
        (org-bleve-index--after-save)
        (should-not called))))
  (with-temp-buffer
    (org-mode)
    (setq-local buffer-file-name "/tmp/test.org")
    (let ((org-bleve-index-mode t)
          (org-bleve-index-auto-sync nil)
          (called nil))
      (cl-letf (((symbol-function 'org-bleve-index-update-buffer)
                 (lambda (_silent-success)
                   (setq called t))))
        (org-bleve-index--after-save)
        (should-not called)))))

;;; org-bleve-index-test.el ends here
