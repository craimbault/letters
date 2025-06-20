package letters

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
)

func normalizeMultilineString(s string) string {
	s = strings.Replace(s, "\r\n", "\n", -1)
	s = strings.Trim(s, "\n ")
	return s
}

func normalizeParametrizedAttributeValue(s string) string {
	s = strings.Trim(s, " ")
	s = strings.ToLower(s)
	return s
}

func ParseDateHeader(s string) time.Time {
	var t time.Time

	formats := []string{
		time.RFC1123Z,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		time.RFC1123Z + " (MST)",
		"Mon, 2 Jan 2006 15:04:05 -0700 (MST)",
	}

	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t
		}
	}

	return t
}

func ParseStringHeader(s string) string {
	decodedHeader, _ := decodeHeader(s)
	return strings.Trim(decodedHeader, " ")
}

func ParseCommaSeparatedStringHeader(s string) []string {
	var values []string

	normalizedS := normalizeMultilineString(s)
	if normalizedS == "" {
		return values
	}

	for _, value := range strings.Split(s, ",") {
		values = append(values, ParseStringHeader(value))
	}
	return values
}

func ParseAddressHeader(
	header mail.Header,
	name string,
) (*mail.Address, error) {
	var address *mail.Address

	ss, ok := header[name]
	if !ok {
		return address, nil
	}

	s := strings.Join(ss, ", ")

	normalizedS := normalizeMultilineString(s)
	if normalizedS == "" {
		return address, nil
	}

	decodedHeader, err := decodeHeader(normalizedS)
	if err != nil {
		return address, fmt.Errorf(
			"letters.parsers.parseAddressHeader: "+
				"cannot decode address header %q: %w",
			s,
			err,
		)
	}

	address, err = mail.ParseAddress(decodedHeader)
	if err != nil {
		return address, fmt.Errorf(
			"letters.parsers.parseAddressHeader: "+
				"cannot parse address header %q: %w",
			s,
			err,
		)
	}

	return address, nil
}

func ParseAddressListHeader(
	header mail.Header,
	name string,
) ([]*mail.Address, error) {
	var addresses []*mail.Address

	ss, ok := header[name]
	if !ok {
		return addresses, nil
	}
	s := strings.Join(ss, ", ")
	normalizedS := normalizeMultilineString(s)
	if normalizedS == "" {
		return addresses, nil
	}

	decodedHeader, err := decodeHeader(normalizedS)
	if err != nil {
		return addresses, fmt.Errorf(
			"letters.parsers.parseAddressListHeader: "+
				"cannot decode address list header %q: %w",
			s,
			err,
		)
	}

	addresses, err = mail.ParseAddressList(decodedHeader)
	if err != nil {
		return addresses, fmt.Errorf(
			"letters.parsers.parseAddressListHeader: "+
				"cannot parse address list header %q: %w",
			s,
			err,
		)
	}

	return addresses, nil
}

func ParseMessageIdHeader(s string) MessageId {
	return MessageId(strings.Trim(s, "<> \n"))
}

func ParseCommaSeparatedMessageIdHeader(s string) []MessageId {
	var values []MessageId

	for _, value := range strings.Split(s, " ") {
		messageId := ParseMessageIdHeader(value)
		if messageId != "" {
			values = append(values, messageId)
		}
	}

	return values
}

func ParseContentDisposition(s string) (ContentDispositionHeader, error) {
	var cdh ContentDispositionHeader

	label, params, err := mime.ParseMediaType(s)
	if label == "" {
		return cdh, nil
	}
	if err != nil {
		return cdh, fmt.Errorf(
			"letters.parsers.parseContentDisposition: "+
				"cannot parse Content-Disposition %q: %w",
			s,
			err,
		)
	}

	cd, ok := cdMap[label]
	if !ok {
		return cdh, fmt.Errorf(
			"letters.parsers.parseContentDisposition: "+
				"unknown Content-Disposition %q",
			label,
		)
	}
	return ContentDispositionHeader{
		ContentDisposition: cd,
		Params:             params,
	}, nil
}

func ParseContentDescription(s string) (ContentDescriptionHeader, error) {
	return ContentDescriptionHeader(strings.Trim(s, "\n")), nil
}

func ParseContentTransferEncoding(s string) (ContentTransferEncoding, error) {
	label := normalizeParametrizedAttributeValue(s)
	if label == "" {
		return cte7bit, nil
	}

	cte, ok := cteMap[label]
	if !ok {
		return cte, fmt.Errorf(
			"letters.parsers.parseContentTransferEncoding: "+
				"unknown Content-Transfer-Encoding %q",
			label,
		)
	}
	return cte, nil
}

func ParseDefaultMediaType(s string) (string, map[string]string, error) {
	if s == "" {
		s = "text/plain"
	}
	mediatype, params, err := mime.ParseMediaType(s)
	if err != nil {
		return mediatype, params, fmt.Errorf(
			"letters.parsers.parseDefaultMediaType: "+
				"cannot parse Content-Type %q: %w",
			s,
			err,
		)
	}
	return mediatype, params, nil
}

func ParseContentTypeHeader(s string) (ContentTypeHeader, error) {
	var cth ContentTypeHeader

	mediaType, mediaTypeParams, err := ParseDefaultMediaType(s)
	if err != nil {
		return cth, fmt.Errorf(
			"letters.parsers.parseContentTypeHeader: "+
				"cannot parse Content-Type %q: %w",
			s,
			err,
		)
	}

	for _, param := range []string{"charset", "micalg", "protocol"} {
		if mediaTypeParams[param] != "" {
			mediaTypeParams[param] = normalizeParametrizedAttributeValue(
				mediaTypeParams[param],
			)
		}
	}
	return ContentTypeHeader{
		ContentType: mediaType,
		Params:      mediaTypeParams,
	}, nil
}

func (ep *EmailParser) ParseHeaders(header mail.Header) (Headers, error) {
	contentType, err := ep.headersParsers.ContentType(
		header.Get("Content-Type"),
	)
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Content-Type: %w",
			err)
	}

	contentDisposition, _ := ep.headersParsers.ContentDisposition(
		header.Get("Content-Disposition"),
	)

	contentDescription, _ := ep.headersParsers.ContentDescription(
		header.Get("Content-Description"),
	)

	extraHeaders := make(map[string][]string)
	for key, value := range header {
		_, isKnownHeader := knownHeaders[key]
		if isKnownHeader {
			continue
		}

		normalisedVals := []string{}
		extraHeaderParserFn,
			hasExtraHeaderParseFn := ep.headersParsers.ExtraHeaders[strings.ToLower(key)]
		for _, val := range value {
			if !hasExtraHeaderParseFn {
				decodedHeader, _ := decodeHeader(val)
				normalisedVals = append(normalisedVals, decodedHeader)
			} else {
				decodedHeader := extraHeaderParserFn(val)
				normalisedVals = append(normalisedVals, decodedHeader)
			}
		}
		extraHeaders[key] = normalisedVals
	}

	sender, err := ep.headersParsers.Sender(header, "Sender")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Sender header: %w",
			err,
		)
	}

	from, err := ep.headersParsers.From(header, "From")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse From header: %w",
			err,
		)
	}

	replyTo, err := ep.headersParsers.ReplyTo(header, "Reply-To")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Reply-To header: %w",
			err,
		)
	}

	to, err := ep.headersParsers.To(header, "To")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse To header: %w",
			err,
		)
	}

	cc, err := ep.headersParsers.Cc(header, "Cc")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Cc header: %w",
			err,
		)
	}

	bcc, err := ep.headersParsers.Bcc(header, "Bcc")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Bcc header: %w",
			err,
		)
	}

	resentFrom, err := ep.headersParsers.ResentFrom(header, "Resent-From")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Resent-From header: %w",
			err,
		)
	}

	resentSender, err := ep.headersParsers.ResentSender(header, "Resent-Sender")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Resent-Sender header: %w",
			err,
		)
	}

	resentTo, err := ep.headersParsers.ResentTo(header, "Resent-To")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Resent-To header: %w",
			err,
		)
	}

	resentCc, err := ep.headersParsers.ResentCc(header, "Resent-Cc")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Resent-Cc header: %w",
			err,
		)
	}

	resentBcc, err := ep.headersParsers.ResentBcc(header, "Resent-Bcc")
	if err != nil {
		return Headers{}, fmt.Errorf(
			"letters.parsers.ParseHeaders: "+
				"cannot parse Resent-Bcc header: %w",
			err,
		)
	}

	return Headers{
		Date:    ep.headersParsers.Date(header.Get("Date")),
		Sender:  sender,
		From:    from,
		ReplyTo: replyTo,
		To:      to,
		Cc:      cc,
		Bcc:     bcc,
		MessageID: ep.headersParsers.MessageID(
			header.Get("Message-ID"),
		),
		InReplyTo: ep.headersParsers.InReplyTo(
			header.Get("In-Reply-To"),
		),
		References: ep.headersParsers.References(
			header.Get("References"),
		),
		Subject:  ep.headersParsers.Subject(header.Get("Subject")),
		Comments: ep.headersParsers.Comments(header.Get("Comments")),
		Keywords: ep.headersParsers.Keywords(header.Get("Keywords")),
		ResentDate: ep.headersParsers.ResentDate(
			header.Get("Resent-Date"),
		),
		ResentFrom:   resentFrom,
		ResentSender: resentSender,
		ResentTo:     resentTo,
		ResentCc:     resentCc,
		ResentBcc:    resentBcc,
		ResentMessageID: ep.headersParsers.ResentMessageID(
			header.Get("Resent-Message-ID"),
		),
		ContentType:        contentType,
		ContentDisposition: contentDisposition,
		ContentDescription: contentDescription,
		ExtraHeaders:       extraHeaders,
	}, nil
}

func parseText(
	t io.Reader,
	e encoding.Encoding,
	cte ContentTransferEncoding,
) (string, error) {
	reader, err := decodeContent(t, e, cte)
	if err != nil {
		return "", fmt.Errorf(
			"letters.parsers.parseText: "+
				"cannot decode plain text content: %w",
			err,
		)
	}

	textBody, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf(
			"letters.parsers.parseText: "+
				"cannot read plain text content: %w",
			err,
		)
	}

	return strings.TrimSuffix(string(textBody), "\n"), nil
}

func isInlineFile(
	contentType ContentTypeHeader,
	parentContentType ContentTypeHeader,
	cdh ContentDispositionHeader,
) bool {
	if cdh.ContentDisposition == ContentDispositionInline {
		return true
	}
	if contentType.ContentType == contentTypeTextPlain ||
		contentType.ContentType == contentTypeTextEnriched ||
		contentType.ContentType == contentTypeTextHtml {
		return false
	}
	return parentContentType.ContentType == contentTypeMultipartRelated
}

func isAttachedFile(
	contentType ContentTypeHeader,
	parentContentType ContentTypeHeader,
) bool {
	if contentType.ContentType != contentTypeTextPlain &&
		contentType.ContentType != contentTypeTextEnriched &&
		contentType.ContentType != contentTypeTextHtml {
		return true
	}
	return parentContentType.ContentType == contentTypeMultipartMixed ||
		parentContentType.ContentType == contentTypeMultipartParallel
}

func (ep *EmailParser) parsePart(
	msg io.Reader,
	parentContentType ContentTypeHeader,
	boundary string,
) (emailBodies, error) {
	var emailBodies emailBodies

	multipartReader := multipart.NewReader(msg, boundary)
	if multipartReader == nil {
		return emailBodies, nil
	}

	for {
		part, err := multipartReader.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			return emailBodies, fmt.Errorf(
				"letters.parsers.parsePart: cannot read part: %w",
				err,
			)
		}

		partContentType, err := ParseContentTypeHeader(
			part.Header.Get("Content-Type"),
		)
		if err != nil {
			return emailBodies, fmt.Errorf(
				"letters.parsers.parsePart: "+
					"cannot parse Content-Type: %w",
				err,
			)
		}

		charsetLabel := partContentType.Params["charset"]
		if charsetLabel == "" {
			charsetLabel = parentContentType.Params["charset"]
		}

		enc, _ := charset.Lookup(charsetLabel)
		cte, err := ParseContentTransferEncoding(
			part.Header.Get("Content-Transfer-Encoding"),
		)
		if err != nil {
			return emailBodies, fmt.Errorf(
				"letters.parsers.parsePart: "+
					"cannot parse Content-Transfer-Encoding: %w",
				err,
			)
		}

		cdh, err := ParseContentDisposition(
			part.Header.Get("Content-Disposition"),
		)
		if err != nil {
			return emailBodies, fmt.Errorf(
				"letters.parsers.parsePart: "+
					"cannot parse Content-Disposition: %w",
				err,
			)
		}
		if cdh.ContentDisposition == ContentDispositionAttachment {
			if !ep.fileFilter(partContentType, cdh) {
				continue
			}

			attachedFile, err := decodeAttachedFileFromPart(part, cte)
			if err != nil {
				return emailBodies, fmt.Errorf(
					"letters.parsers.parsePart: "+
						"cannot decode attached file: %w",
					err,
				)
			}
			emailBodies.AttachedFiles = append(
				emailBodies.AttachedFiles,
				attachedFile,
			)
			continue
		}

		if partContentType.ContentType == contentTypeTextPlain {
			if !ep.bodyFilter(partContentType) {
				continue
			}

			partTextBody, err := parseText(part, enc, cte)
			if err != nil {
				return emailBodies, fmt.Errorf(
					"letters.parsers.parsePart: "+
						"cannot parse plain text: %w",
					err,
				)
			}
			emailBodies.text += partTextBody
			emailBodies.text += "\n\n"
			continue
		}

		if partContentType.ContentType == contentTypeTextEnriched {
			if !ep.bodyFilter(partContentType) {
				continue
			}

			partEnrichedText, err := parseText(part, enc, cte)
			if err != nil {
				return emailBodies, fmt.Errorf(
					"letters.parsers.parsePart: "+
						"cannot parse enriched text: %w",
					err,
				)
			}
			emailBodies.enrichedText += partEnrichedText
			continue
		}

		if partContentType.ContentType == contentTypeTextHtml {
			if !ep.bodyFilter(partContentType) {
				continue
			}

			partHtmlBody, err := parseText(part, enc, cte)
			if err != nil {
				return emailBodies, fmt.Errorf(
					"letters.parsers.parsePart: "+
						"cannot parse html text: %w",
					err,
				)
			}
			emailBodies.html += partHtmlBody
			continue
		}

		if strings.HasPrefix(
			partContentType.ContentType,
			contentTypeMultipartPrefix,
		) {
			nestedEmailBodies, err := ep.parsePart(
				part,
				partContentType,
				partContentType.Params["boundary"],
			)
			if err != nil {
				return emailBodies, fmt.Errorf(
					"letters.parsers.parsePart: "+
						"cannot parse nested part: %w",
					err,
				)
			}

			emailBodies.extend(nestedEmailBodies)
			continue
		}

		if isInlineFile(partContentType, parentContentType, cdh) {
			if !ep.fileFilter(partContentType, cdh) {
				continue
			}

			inlineFile, err := decodeInlineFile(part, cte)
			if err != nil {
				return emailBodies, fmt.Errorf(
					"letters.parsers.parsePart: "+
						"cannot decode inline file: %w",
					err,
				)
			}
			emailBodies.InlineFiles = append(
				emailBodies.InlineFiles,
				inlineFile,
			)
			continue
		}

		if isAttachedFile(partContentType, parentContentType) {
			if !ep.fileFilter(partContentType, cdh) {
				continue
			}

			attachedFile, err := decodeAttachedFileFromPart(part, cte)
			if err != nil {
				return emailBodies, fmt.Errorf(
					"letters.parsers.parsePart: "+
						"cannot decode attached file: %w",
					err,
				)
			}
			emailBodies.AttachedFiles = append(
				emailBodies.AttachedFiles,
				attachedFile,
			)
			continue
		}

		return emailBodies, &UnknownContentTypeError{
			contentType: parentContentType.ContentType,
		}
	}

	emailBodies.text = strings.Trim(emailBodies.text, "\n")
	emailBodies.enrichedText = strings.Trim(emailBodies.enrichedText, "\n")
	emailBodies.html = strings.Trim(emailBodies.html, "\n")

	return emailBodies, nil
}
