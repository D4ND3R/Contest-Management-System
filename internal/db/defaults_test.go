package db

import (
	"reflect"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// fill sets every field of the struct v points to a non-zero value.
func fill(v reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x" + v.Type().Field(i).Name)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int32, reflect.Int64:
			f.SetInt(int64(i + 7))
		case reflect.Slice:
			if f.Type().Elem().Kind() == reflect.String {
				f.Set(reflect.ValueOf([]string{"a"}))
			} else {
				f.Set(reflect.MakeSlice(f.Type(), 1, 1))
			}
		case reflect.Pointer:
			p := reflect.New(f.Type().Elem())
			if p.Elem().Type() == reflect.TypeOf(time.Time{}) {
				p.Elem().Set(reflect.ValueOf(time.Unix(int64(i), 0)))
			} else if p.Elem().Kind() == reflect.Int32 || p.Elem().Kind() == reflect.Int64 {
				p.Elem().SetInt(int64(i + 3))
			}
			f.Set(p)
		case reflect.Struct:
			if f.Type() == reflect.TypeOf(time.Time{}) {
				f.Set(reflect.ValueOf(time.Unix(int64(i)+100, 0)))
			}
		}
	}
}

// TestRowToUpdateCopiesEveryField: the *ToUpdate helpers copy every field
// the update query sets, so an edit form never resets a column it does not
// show (a column added to the query but forgotten here fails this test).
func TestRowToUpdateCopiesEveryField(t *testing.T) {
	cases := []struct {
		row  any
		conv func(any) any
	}{
		{&sqlc.Contest{}, func(r any) any { return ContestToUpdate(*r.(*sqlc.Contest)) }},
		{&sqlc.Task{}, func(r any) any { return TaskToUpdate(*r.(*sqlc.Task)) }},
		{&sqlc.Dataset{}, func(r any) any { return DatasetToUpdate(*r.(*sqlc.Dataset)) }},
		{&sqlc.Participation{}, func(r any) any { return ParticipationToUpdate(*r.(*sqlc.Participation)) }},
	}
	for _, c := range cases {
		row := reflect.ValueOf(c.row).Elem()
		fill(row)
		up := reflect.ValueOf(c.conv(c.row))
		for i := 0; i < up.NumField(); i++ {
			name := up.Type().Field(i).Name
			src := row.FieldByName(name)
			if !src.IsValid() {
				t.Errorf("%s.%s has no source column", up.Type().Name(), name)
				continue
			}
			if !reflect.DeepEqual(src.Interface(), up.Field(i).Interface()) {
				t.Errorf("%s.%s is not copied", up.Type().Name(), name)
			}
		}
	}
}
