;;; org-bleve-index.el --- Keep an external Bleve index up to date for Org files -*- lexical-binding: t; -*-

;; Copyright (C) 2026 Peter Solodov

;; Author: Peter Solodov
;; Keywords: outlines, tools
;; Package-Requires: ((emacs "28.1"))
;; Version: 0.1.0

;;; Commentary:

;; org-bleve-index keeps an external Bleve index maintained by the `org-search`
;; binary in sync with file-backed Org buffers.
;;
;; To integrate it into Emacs:
;;
;; 1. Install the `org-search` binary and make it available on PATH, or set
;;    `org-bleve-index-command` to the absolute binary path.
;; 2. Put your org-search txtpb config in the default XDG location, or set
;;    `org-bleve-index-config-file` to the config file you want Emacs to use.
;; 3. Load this file and enable `org-bleve-index-mode`.
;;
;; Minimal setup:
;;
;;   (add-to-list 'load-path "/path/to/org-search/emacs")
;;   (require 'org-bleve-index)
;;   (org-bleve-index-mode 1)
;;
;; With `org-bleve-index-mode` enabled, saves in file-backed Org buffers trigger
;; asynchronous `org-search update-file --json ...` commands. Manual rebuild is
;; available through `org-bleve-index-rebuild`, and the diagnostics buffer can
;; be opened with `org-bleve-index-open-diagnostics`.
;;
;; This package intentionally keeps a narrow scope: it maintains the external
;; Bleve index, but it does not provide interactive search functionality inside
;; Emacs.

;;; Code:

(require 'cl-lib)
(require 'subr-x)

(defgroup org-bleve-index nil
  "Keep an external Bleve index up to date for Org files."
  :group 'org)

(defcustom org-bleve-index-command "org-search"
  "Command used to launch the org-search binary."
  :type 'string
  :group 'org-bleve-index)

(defcustom org-bleve-index-config-file nil
  "Optional txtpb config file passed to org-search.

Nil leaves config discovery to org-search, which uses its default XDG config
location."
  :type '(choice (const :tag "Use org-search default config discovery" nil)
                 (file :must-match nil))
  :group 'org-bleve-index)

(defcustom org-bleve-index-auto-sync t
  "Whether `org-bleve-index-mode' should update the index after Org saves."
  :type 'boolean
  :group 'org-bleve-index)

(defcustom org-bleve-index-diagnostics-buffer "*org-bleve-index*"
  "Buffer used to collect raw process output for failed org-search commands."
  :type 'string
  :group 'org-bleve-index)

(define-minor-mode org-bleve-index-mode
  "Keep an external Bleve index up to date for file-backed Org buffers.

When enabled, file-backed Org buffers trigger asynchronous
`org-search update-file --json ...' commands after save."
  :global t
  :lighter " OrgBleve"
  (if org-bleve-index-mode
      (add-hook 'after-save-hook #'org-bleve-index--after-save)
    (remove-hook 'after-save-hook #'org-bleve-index--after-save)))

(defun org-bleve-index-open-diagnostics ()
  "Display the diagnostics buffer used for failed org-search commands."
  (interactive)
  (pop-to-buffer (get-buffer-create org-bleve-index-diagnostics-buffer)))

(defun org-bleve-index-update-buffer (&optional silent-success)
  "Update the external index for the current file-backed Org buffer.

With SILENT-SUCCESS non-nil, suppress the success minibuffer message."
  (interactive)
  (org-bleve-index--ensure-org-file-buffer)
  (org-bleve-index--start-command
   "update-file"
   (list (expand-file-name buffer-file-name))
   silent-success))

(defun org-bleve-index-rebuild (&optional silent-success)
  "Rebuild the external Org Bleve index.

With SILENT-SUCCESS non-nil, suppress the success minibuffer message."
  (interactive)
  (org-bleve-index--start-command "rebuild" nil silent-success))

(defun org-bleve-index--after-save ()
  (when (and org-bleve-index-mode
             org-bleve-index-auto-sync
             buffer-file-name
             (derived-mode-p 'org-mode))
    (org-bleve-index-update-buffer t)))

(defun org-bleve-index--ensure-org-file-buffer ()
  (unless (derived-mode-p 'org-mode)
    (user-error "org-bleve-index requires an Org buffer"))
  (unless buffer-file-name
    (user-error "org-bleve-index requires a file-backed buffer")))

(defun org-bleve-index--start-command (subcommand arguments silent-success)
  (let* ((args (org-bleve-index--command-args subcommand arguments))
         (process-buffer (generate-new-buffer (format " *org-bleve-index-%s*" subcommand)))
         (process (apply #'start-process
                         (format "org-bleve-index-%s" subcommand)
                         process-buffer
                         org-bleve-index-command
                         args)))
    (process-put process 'org-bleve-index-subcommand subcommand)
    (process-put process 'org-bleve-index-args args)
    (process-put process 'org-bleve-index-silent-success silent-success)
    (set-process-query-on-exit-flag process nil)
    (set-process-sentinel process #'org-bleve-index--process-sentinel)
    (unless silent-success
      (message "org-bleve-index: started %s" subcommand))
    process))

(defun org-bleve-index--command-args (subcommand arguments)
  (append (list "--json")
          (when org-bleve-index-config-file
            (list "--config" (expand-file-name org-bleve-index-config-file)))
          (list subcommand)
          arguments))

(defun org-bleve-index--process-sentinel (process _event)
  (when (memq (process-status process) '(exit signal))
    (let ((subcommand (process-get process 'org-bleve-index-subcommand))
          (silent-success (process-get process 'org-bleve-index-silent-success))
          (success (and (eq (process-status process) 'exit)
                        (zerop (process-exit-status process)))))
      (unwind-protect
          (if success
              (unless silent-success
                (message "org-bleve-index: %s finished" subcommand))
            (org-bleve-index--record-failure process)
            (message "org-bleve-index: %s failed; see %s"
                     subcommand
                     org-bleve-index-diagnostics-buffer))
        (when-let ((buffer (process-buffer process)))
          (when (buffer-live-p buffer)
            (kill-buffer buffer)))))))

(defun org-bleve-index--record-failure (process)
  (let ((output (org-bleve-index--process-output process))
        (command-line (string-join
                       (cons org-bleve-index-command
                             (process-get process 'org-bleve-index-args))
                       " ")))
    (with-current-buffer (get-buffer-create org-bleve-index-diagnostics-buffer)
      (goto-char (point-max))
      (unless (bobp)
        (insert "\n"))
      (insert (format "$ %s\n" command-line))
      (insert output)
      (unless (string-suffix-p "\n" output)
        (insert "\n")))))

(defun org-bleve-index--process-output (process)
  (if-let ((buffer (process-buffer process)))
      (with-current-buffer buffer
        (buffer-substring-no-properties (point-min) (point-max)))
    ""))

(provide 'org-bleve-index)

;;; org-bleve-index.el ends here
